// Package runtime implements the HookScript automation engine using frankenphp.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dunglas/frankenphp"
	"github.com/webong/gateway/driver/automation"
)

const Language automation.Language = "hookscript"

type Engine struct {
	timeout       time.Duration
	maxSourceBytes int64
	initialized   bool
}

func New(opts ...Option) *Engine {
	e := &Engine{
		timeout:        5 * time.Second,
		maxSourceBytes: 65536,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *Engine) Language() automation.Language { return Language }

type Option func(*Engine)

func WithTimeout(d time.Duration) Option {
	return func(e *Engine) { e.timeout = d }
}

func WithMaxSourceBytes(n int64) Option {
	return func(e *Engine) { e.maxSourceBytes = n }
}

// Execute runs a HookScript using frankenphp.
func (e *Engine) Execute(ctx context.Context, source string, input automation.Input) (automation.Outcome, error) {
	if strings.TrimSpace(source) == "" {
		return automation.Outcome{}, fmt.Errorf("hookscript source is required")
	}
	if int64(len(source)) > e.maxSourceBytes {
		return automation.Outcome{}, fmt.Errorf("hookscript source exceeds configured limit")
	}

	// Initialize frankenphp once
	if !e.initialized {
		if err := frankenphp.Init(); err != nil {
			return automation.Outcome{}, fmt.Errorf("initialize frankenphp: %w", err)
		}
		e.initialized = true
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	// Build the PHP script
	script := e.buildScript(source, input)

	// Execute via frankenphp
	return e.executeScript(ctx, script)
}

func (e *Engine) buildScript(source string, input automation.Input) string {
	// Prepare variables for the script
	vars := map[string]any{
		"variables": input.Variables,
		"event":     input.Event,
	}

	varsJSON, _ := json.Marshal(vars)

	return fmt.Sprintf(`
<?php
require_once __DIR__ . '/vendor/autoload.php';

use Smuuf\Primi\Context;
use Smuuf\Primi\Interpreter;

$input = %s;
$source = <<<'EOT'
%s
EOT;

$host = new Webong\Gateway\HookScript\Host($input['event'], $input['variables']);
$context = new Context();
$interpreter = new Interpreter($context);
$context->setVariables($host->functions());
$context->setVariable('variables', $host->scriptVariables());

try {
    $interpreter->run($source);
} catch (WebhookScriptResponseReady $e) {
    // Response thrown, continue
} catch (WebhookScriptTimedOut $e) {
    throw new RuntimeException('HookScript execution timed out');
} catch (Throwable $e) {
    throw new RuntimeException('HookScript execution failed: ' . $e->getMessage(), 0, $e);
}

$outcome = $host->outcome();
echo json_encode($outcome);
`, string(varsJSON), source)
}

func (e *Engine) executeScript(ctx context.Context, script string) (automation.Outcome, error) {
	// Write script to temp file
	tmpFile, err := os.CreateTemp("", "hookscript-*.php")
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(script); err != nil {
		return automation.Outcome{}, fmt.Errorf("write script: %w", err)
	}
	tmpFile.Close()

	// Execute via frankenphp
	output, err := executePhpScript(ctx, tmpFile.Name())
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("execute script: %w", err)
	}

	// Parse the JSON output
	var outcome automation.Outcome
	if err := json.Unmarshal(output, &outcome); err != nil {
		return automation.Outcome{}, fmt.Errorf("parse outcome: %w", err)
	}

	return outcome, nil
}

func executePhpScript(ctx context.Context, scriptPath string) ([]byte, error) {
	// Use frankenphp's ExecuteScript function
	result, err := frankenphp.ExecuteScript(scriptPath, nil)
	if err != nil {
		return nil, err
	}
	return result, nil
}

var _ automation.Engine = (*Engine)(nil)
// Plugin returns the engine for composition roots and dynamic loaders.
func Plugin() (automation.Engine, error) { return New(), nil }
