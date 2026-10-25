// Package runtime implements the optional local PHP automation extension.
// PHP is launched by the extension process; Gateway hosts remain PHP-free.
package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webong/gateway/driver/automation"
)

const Language automation.Language = "php"

const defaultMaxOutputBytes int64 = 1 << 20

type Engine struct {
	Binary         string
	Timeout        time.Duration
	MaxSourceBytes int64
	MaxOutputBytes int64
}

type Option func(*Engine)

func WithBinary(binary string) Option { return func(engine *Engine) { engine.Binary = binary } }
func WithTimeout(timeout time.Duration) Option {
	return func(engine *Engine) { engine.Timeout = timeout }
}
func WithMaxSourceBytes(limit int64) Option {
	return func(engine *Engine) { engine.MaxSourceBytes = limit }
}
func WithMaxOutputBytes(limit int64) Option {
	return func(engine *Engine) { engine.MaxOutputBytes = limit }
}

func New(options ...Option) *Engine {
	engine := &Engine{
		Binary: "php", Timeout: 5 * time.Second,
		MaxSourceBytes: 65536, MaxOutputBytes: defaultMaxOutputBytes,
	}
	for _, option := range options {
		option(engine)
	}
	return engine
}

func NewFromEnvironment() (*Engine, error) {
	engine := New()
	if value := strings.TrimSpace(os.Getenv("GATEWAY_PHP_AUTOMATION_BINARY")); value != "" {
		engine.Binary = value
	}
	if value := strings.TrimSpace(os.Getenv("GATEWAY_PHP_AUTOMATION_TIMEOUT")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 || parsed > 5*time.Minute {
			return nil, errors.New("GATEWAY_PHP_AUTOMATION_TIMEOUT must be between 1ns and 5m")
		}
		engine.Timeout = parsed
	}
	if value := strings.TrimSpace(os.Getenv("GATEWAY_PHP_AUTOMATION_MAX_SOURCE_BYTES")); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 || parsed > 16<<20 {
			return nil, errors.New("GATEWAY_PHP_AUTOMATION_MAX_SOURCE_BYTES must be between 1 and 16777216")
		}
		engine.MaxSourceBytes = parsed
	}
	if value := strings.TrimSpace(os.Getenv("GATEWAY_PHP_AUTOMATION_MAX_OUTPUT_BYTES")); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 || parsed > 16<<20 {
			return nil, errors.New("GATEWAY_PHP_AUTOMATION_MAX_OUTPUT_BYTES must be between 1 and 16777216")
		}
		engine.MaxOutputBytes = parsed
	}
	path, err := exec.LookPath(engine.Binary)
	if err != nil {
		return nil, fmt.Errorf("resolve PHP automation executable: %w", err)
	}
	engine.Binary = path
	return engine, nil
}

func (engine *Engine) Language() automation.Language { return Language }

func (engine *Engine) Execute(ctx context.Context, source string, input automation.Input) (automation.Outcome, error) {
	if strings.TrimSpace(source) == "" {
		return automation.Outcome{}, errors.New("php source is required")
	}
	if int64(len(source)) > engine.MaxSourceBytes {
		return automation.Outcome{}, errors.New("php source exceeds configured limit")
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("encode PHP automation input: %w", err)
	}
	directory, err := os.MkdirTemp("", "gateway-php-automation-")
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("create PHP automation directory: %w", err)
	}
	defer os.RemoveAll(directory)
	scriptPath := directory + string(os.PathSeparator) + "run.php"
	outputPath := directory + string(os.PathSeparator) + "outcome.json"
	if err := os.WriteFile(scriptPath, []byte(buildScript(source, inputJSON)), 0o600); err != nil {
		return automation.Outcome{}, fmt.Errorf("write PHP automation script: %w", err)
	}
	timeout := engine.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, engine.Binary, "-n", scriptPath, outputPath)
	command.Env = []string{"PATH=" + os.Getenv("PATH")}
	diagnostic := &limitedDiagnostic{limit: 4096}
	command.Stdout = diagnostic
	command.Stderr = diagnostic
	if err := command.Run(); err != nil {
		if runCtx.Err() != nil {
			return automation.Outcome{}, fmt.Errorf("execute PHP automation: %w", runCtx.Err())
		}
		return automation.Outcome{}, fmt.Errorf("execute PHP automation: %w: %s", err, diagnostic.String())
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		return automation.Outcome{}, errors.New("PHP automation returned no outcome")
	}
	limit := engine.MaxOutputBytes
	if limit <= 0 {
		limit = defaultMaxOutputBytes
	}
	if info.Size() > limit {
		return automation.Outcome{}, errors.New("PHP automation outcome exceeds configured limit")
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("read PHP automation outcome: %w", err)
	}
	var outcome automation.Outcome
	if err := json.Unmarshal(output, &outcome); err != nil {
		return automation.Outcome{}, fmt.Errorf("decode PHP automation outcome: %w", err)
	}
	return outcome, nil
}

func buildScript(source string, input []byte) string {
	return `<?php
declare(strict_types=1);
final class GatewayResponseReady extends Exception {}
final class GatewayHost {
    public array $variables;
    public array $deliveries = [];
    public array $schedules = [];
    public ?array $response = null;
    public function __construct(public readonly array $event, array $variables) { $this->variables = $variables; }
    public function get(string $path, mixed $default = null): mixed {
        $value = $this->variables;
        foreach (explode('.', $path) as $segment) {
            if (!is_array($value) || !array_key_exists($segment, $value)) return $default;
            $value = $value[$segment];
        }
        return $value;
    }
    public function set(string $path, mixed $value): void {
        $segments = explode('.', $path); $target =& $this->variables;
        foreach (array_slice($segments, 0, -1) as $segment) {
            if (!isset($target[$segment]) || !is_array($target[$segment])) $target[$segment] = [];
            $target =& $target[$segment];
        }
        $target[$segments[count($segments) - 1]] = $value;
    }
    public function respond(mixed $body = '', int $status = 200, array $headers = []): never {
        $this->response = ['status' => $status, 'body' => is_string($body) ? $body : json_encode($body, JSON_THROW_ON_ERROR)];
        if ($headers !== []) $this->response['headers'] = $this->normalizeHeaders($headers);
        throw new GatewayResponseReady();
    }
    public function forward(array $delivery): void {
        if (isset($delivery['headers']) && is_array($delivery['headers'])) {
            if ($delivery['headers'] === []) unset($delivery['headers']);
            else $delivery['headers'] = $this->normalizeHeaders($delivery['headers']);
        }
        $this->deliveries[] = $delivery;
    }
    public function schedule(array $schedule): void { $this->schedules[] = $schedule; }
    public function outcome(): array { return ['Variables' => $this->variables, 'Deliveries' => $this->deliveries, 'Schedules' => $this->schedules, 'Response' => $this->response]; }
    private function normalizeHeaders(array $headers): array {
        $normalized = [];
        foreach ($headers as $name => $values) {
            $normalized[(string) $name] = array_map(static fn (mixed $value): string => (string) $value, is_array($values) ? $values : [$values]);
        }
        return $normalized;
    }
}
$input = json_decode(base64_decode('` + base64.StdEncoding.EncodeToString(input) + `', true), true, 128, JSON_THROW_ON_ERROR);
$gateway = new GatewayHost($input['Event'] ?? $input['event'] ?? [], $input['Variables'] ?? $input['variables'] ?? []);
try {
    (static function (GatewayHost $gateway): void {
` + source + `
    })($gateway);
} catch (GatewayResponseReady) {
}
file_put_contents($argv[1], json_encode($gateway->outcome(), JSON_THROW_ON_ERROR), LOCK_EX);
`
}

type limitedDiagnostic struct {
	mu    sync.Mutex
	limit int
	value []byte
}

func (diagnostic *limitedDiagnostic) Write(value []byte) (int, error) {
	diagnostic.mu.Lock()
	defer diagnostic.mu.Unlock()
	remaining := diagnostic.limit - len(diagnostic.value)
	if remaining > len(value) {
		remaining = len(value)
	}
	if remaining > 0 {
		diagnostic.value = append(diagnostic.value, value[:remaining]...)
	}

	return len(value), nil
}

func (diagnostic *limitedDiagnostic) String() string {
	diagnostic.mu.Lock()
	defer diagnostic.mu.Unlock()

	return strings.TrimSpace(string(diagnostic.value))
}

var _ automation.Engine = (*Engine)(nil)

// Plugin returns the optional PHP engine for its installable process package.
func Plugin() (automation.Engine, error) { return NewFromEnvironment() }
