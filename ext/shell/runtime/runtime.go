// Package runtime implements the optional shell-hook automation extension.
package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webong/gateway/driver/automation"
)

const Language automation.Language = "shell"

const (
	defaultTimeout        = 15 * time.Second
	defaultMaxOutputBytes = int64(64 * 1024)
	defaultMaxConcurrent  = 2
	maximumTimeout        = 5 * time.Minute
	maximumOutputBytes    = int64(16 * 1024 * 1024)
	maximumConcurrent     = 128
	maximumQueued         = 1024
	runPruneInterval      = time.Hour
	maxRunRecordOutput    = 64 * 1024
	maxRunRecordBytes     = 1 << 20
)

const (
	ModeOutcome           = "outcome"
	ModeBuffered          = "buffered"
	InterpreterDirect     = "direct"
	InterpreterPowerShell = "powershell"
)

var ErrBusy = automation.ErrBusy

// RunFailure preserves the private run ID when a hook fails. The wrapped
// error remains inspectable by the host, but never enters notifications.
type RunFailure struct {
	ID    string
	Cause error
}

func (failure *RunFailure) Error() string { return failure.Cause.Error() }
func (failure *RunFailure) Unwrap() error { return failure.Cause }
func (failure *RunFailure) RunID() string { return failure.ID }

type Config struct {
	HooksDir        string
	RunLogDir       string
	RunRetention    time.Duration
	ExposeRunOutput bool
	Timeout         time.Duration
	MaxOutputBytes  int64
	MaxConcurrent   int
	MaxQueued       int
	OutputMode      string
	Interpreter     string
	PowerShellPath  string
}

type Engine struct {
	config    Config
	slots     chan struct{}
	queue     chan struct{}
	pruneMu   sync.Mutex
	lastPrune time.Time
}

func New(config Config) (*Engine, error) {
	if strings.TrimSpace(config.HooksDir) == "" {
		return nil, errors.New("shell hook directory is required")
	}
	root, err := filepath.Abs(config.HooksDir)
	if err != nil {
		return nil, fmt.Errorf("resolve shell hook directory: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve shell hook directory symlinks: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat shell hook directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("shell hook directory is not a directory")
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	if config.Timeout > maximumTimeout {
		return nil, fmt.Errorf("shell timeout must not exceed %s", maximumTimeout)
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = defaultMaxOutputBytes
	}
	if config.MaxOutputBytes > maximumOutputBytes {
		return nil, fmt.Errorf("shell output limit must not exceed %d bytes", maximumOutputBytes)
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = defaultMaxConcurrent
	}
	if config.MaxConcurrent > maximumConcurrent {
		return nil, fmt.Errorf("shell concurrency limit must not exceed %d", maximumConcurrent)
	}
	if config.MaxQueued < 0 || config.MaxQueued > maximumQueued {
		return nil, fmt.Errorf("shell queue limit must be between 0 and %d", maximumQueued)
	}
	if config.OutputMode == "" {
		config.OutputMode = ModeOutcome
	}
	if config.OutputMode != ModeOutcome && config.OutputMode != ModeBuffered {
		return nil, errors.New("shell output mode must be outcome or buffered")
	}
	if config.Interpreter == "" {
		config.Interpreter = InterpreterDirect
	}
	if config.Interpreter != InterpreterDirect && config.Interpreter != InterpreterPowerShell {
		return nil, errors.New("shell interpreter must be direct or powershell")
	}
	if config.Interpreter == InterpreterPowerShell {
		if !filepath.IsAbs(config.PowerShellPath) {
			return nil, errors.New("shell PowerShell path must be absolute")
		}
		if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(config.PowerShellPath), ".exe") {
			return nil, errors.New("shell PowerShell path must name an .exe file on Windows")
		}
		path, err := filepath.EvalSymlinks(config.PowerShellPath)
		if err != nil {
			return nil, fmt.Errorf("resolve shell PowerShell executable: %w", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat shell PowerShell executable: %w", err)
		}
		if !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
			return nil, errors.New("shell PowerShell path must be an executable regular file")
		}
		config.PowerShellPath = path
	} else if config.PowerShellPath != "" {
		return nil, errors.New("shell PowerShell path requires powershell interpreter mode")
	}
	if config.RunLogDir != "" {
		logDir, err := filepath.Abs(config.RunLogDir)
		if err != nil {
			return nil, fmt.Errorf("resolve shell run log directory: %w", err)
		}
		info, err := os.Stat(logDir)
		if err != nil {
			return nil, fmt.Errorf("stat shell run log directory: %w", err)
		}
		if !info.IsDir() {
			return nil, errors.New("shell run log directory is not a directory")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("shell run log directory must be accessible only by its owner")
		}
		config.RunLogDir, err = filepath.EvalSymlinks(logDir)
		if err != nil {
			return nil, fmt.Errorf("resolve shell run log directory symlinks: %w", err)
		}
	}
	if config.RunRetention < 0 || (config.RunRetention > 0 && config.RunLogDir == "") {
		return nil, errors.New("shell run retention requires a run log directory and nonnegative duration")
	}
	if config.ExposeRunOutput && config.RunLogDir == "" {
		return nil, errors.New("shell run output exposure requires a run log directory")
	}
	config.HooksDir = root
	engine := &Engine{config: config, slots: make(chan struct{}, config.MaxConcurrent), queue: make(chan struct{}, config.MaxQueued)}
	if err := engine.pruneRuns(time.Now()); err != nil {
		return nil, fmt.Errorf("prune shell run records: %w", err)
	}
	return engine, nil
}

// NewFromEnvironment creates the engine from the process-hook deployment
// settings. Callers decide whether the extension is enabled before invoking it.
func NewFromEnvironment() (*Engine, error) {
	timeout, err := durationEnv("GATEWAY_SHELL_TIMEOUT", defaultTimeout)
	if err != nil {
		return nil, err
	}
	maxOutput, err := int64Env("GATEWAY_SHELL_MAX_OUTPUT_BYTES", defaultMaxOutputBytes)
	if err != nil {
		return nil, err
	}
	maxConcurrent, err := intEnv("GATEWAY_SHELL_MAX_CONCURRENT", defaultMaxConcurrent)
	if err != nil {
		return nil, err
	}
	maxQueued, err := nonnegativeIntEnv("GATEWAY_SHELL_MAX_QUEUED", 0)
	if err != nil {
		return nil, err
	}
	runRetention, err := durationEnv("GATEWAY_SHELL_RUN_RETENTION", 0)
	if err != nil {
		return nil, err
	}
	exposeRunOutput, err := boolEnv("GATEWAY_SHELL_RUN_LOG_EXPOSE_OUTPUT")
	if err != nil {
		return nil, err
	}
	return New(Config{
		HooksDir:        os.Getenv("GATEWAY_SHELL_HOOKS_DIR"),
		RunLogDir:       os.Getenv("GATEWAY_SHELL_RUN_LOG_DIR"),
		RunRetention:    runRetention,
		ExposeRunOutput: exposeRunOutput,
		Timeout:         timeout,
		MaxOutputBytes:  maxOutput,
		MaxConcurrent:   maxConcurrent,
		MaxQueued:       maxQueued,
		OutputMode:      strings.TrimSpace(os.Getenv("GATEWAY_SHELL_OUTPUT_MODE")),
		Interpreter:     strings.TrimSpace(os.Getenv("GATEWAY_SHELL_INTERPRETER")),
		PowerShellPath:  strings.TrimSpace(os.Getenv("GATEWAY_SHELL_POWERSHELL_PATH")),
	})
}

func (e *Engine) Language() automation.Language { return Language }

func (e *Engine) RunHistoryConfigured() bool { return e != nil && e.config.RunLogDir != "" }

// Execute runs one hook selected by its relative source path. It sends the
// Gateway input as JSON on stdin and accepts a JSON Outcome on stdout. The
// hook is never passed to a shell parser.
func (e *Engine) Execute(ctx context.Context, source string, input automation.Input) (outcome automation.Outcome, execErr error) {
	if e == nil {
		return automation.Outcome{}, errors.New("shell hook engine is not configured")
	}
	release, err := e.admit(ctx)
	if err != nil {
		return automation.Outcome{}, err
	}
	defer release()
	started := time.Now().UTC()
	runID, err := newRunID()
	if err != nil {
		return automation.Outcome{}, err
	}
	record := runRecord{ID: runID, Hook: source, StartedAt: started, DeliveryID: eventString(input.Event, "delivery_id"), ExitCode: -1}
	defer func() {
		record.DurationMS = time.Since(started).Milliseconds()
		if execErr != nil {
			record.Error = execErr.Error()
		}
		if err := e.writeRecord(record); err != nil {
			outcome = automation.Outcome{}
			execErr = fmt.Errorf("record shell hook run: %w", err)
		}
		if execErr != nil {
			execErr = &RunFailure{ID: runID, Cause: execErr}
		}
	}()
	path, err := e.hookPath(source)
	if err != nil {
		return automation.Outcome{}, err
	}
	payload, err := json.Marshal(map[string]any{"event": input.Event, "variables": input.Variables})
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("encode shell hook input: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, e.config.Timeout)
	defer cancel()
	command := e.hookCommand(ctx, path)
	configureProcess(command)
	command.Stdin = bytes.NewReader(payload)
	command.Env = e.hookEnvironment()
	command.WaitDelay = 250 * time.Millisecond
	stdout := &limitedBuffer{limit: e.config.MaxOutputBytes}
	stderr := &limitedBuffer{limit: e.config.MaxOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Run()
	record.Stdout = stdout.String()
	record.Stderr = stderr.String()
	if command.ProcessState != nil {
		record.ExitCode = command.ProcessState.ExitCode()
	}
	if err != nil {
		record.Error = err.Error()
	}
	if stdout.exceeded || stderr.exceeded {
		return automation.Outcome{}, fmt.Errorf("shell hook output exceeded %d bytes per stream", e.config.MaxOutputBytes)
	}
	if ctx.Err() != nil {
		return automation.Outcome{}, fmt.Errorf("shell hook timed out: %w", ctx.Err())
	}
	if e.config.OutputMode == ModeBuffered {
		status := 200
		if err != nil {
			status = 500
			if exitCode := record.ExitCode; exitCode >= 100 && exitCode <= 255 {
				status = exitCode + 300
			}
		}
		return automation.Outcome{RunID: runID, RunFailed: err != nil, Response: &automation.Response{
			Status: status, Body: stdout.String(), Headers: map[string][]string{
				"Content-Type":           {"text/plain; charset=utf-8"},
				"X-Gateway-Shell-Run-Id": {runID},
			},
		}}, nil
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return automation.Outcome{}, fmt.Errorf("shell hook failed: %w", err)
		}
		return automation.Outcome{}, fmt.Errorf("shell hook failed: %w: %s", err, message)
	}

	if err := json.Unmarshal(stdout.Bytes(), &outcome); err != nil {
		return automation.Outcome{}, fmt.Errorf("decode shell hook outcome: %w", err)
	}
	if len(outcome.Schedules) > 0 {
		return automation.Outcome{}, errors.New("shell hooks cannot declare schedules")
	}
	if outcome.Response != nil {
		if outcome.Response.Headers == nil {
			outcome.Response.Headers = make(map[string][]string)
		}
		outcome.Response.Headers["X-Gateway-Shell-Run-Id"] = []string{runID}
	}
	outcome.RunID = runID
	return outcome, nil
}

type runRecord struct {
	ID              string    `json:"id"`
	Hook            string    `json:"hook"`
	DeliveryID      string    `json:"delivery_id,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	DurationMS      int64     `json:"duration_ms"`
	ExitCode        int       `json:"exit_code"`
	Stdout          string    `json:"stdout"`
	Stderr          string    `json:"stderr"`
	StdoutTruncated bool      `json:"stdout_truncated,omitempty"`
	StderrTruncated bool      `json:"stderr_truncated,omitempty"`
	Error           string    `json:"error,omitempty"`
}

func newRunID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create shell run ID: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func eventString(event map[string]any, name string) string {
	value, _ := event[name].(string)
	return value
}

// Run records are private host files. The private admin API reads only
// metadata unless an operator explicitly enables output exposure.
func (e *Engine) writeRecord(record runRecord) error {
	if e.config.RunLogDir == "" {
		return nil
	}
	if len(record.Stdout) > maxRunRecordOutput {
		record.Stdout = record.Stdout[:maxRunRecordOutput]
		record.StdoutTruncated = true
	}
	if len(record.Stderr) > maxRunRecordOutput {
		record.Stderr = record.Stderr[:maxRunRecordOutput]
		record.StderrTruncated = true
	}
	if len(record.Error) > 4096 {
		record.Error = record.Error[:4096]
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(e.config.RunLogDir, record.ID+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return e.pruneRuns(time.Now())
}

// ReadRun returns a bounded, private run-history resource. Output and error
// text are omitted by default because hooks may print secrets or request data.
func (e *Engine) ReadRun(id string) ([]byte, error) {
	if e == nil || e.config.RunLogDir == "" || !runRecordFilename(id+".json") {
		return nil, os.ErrNotExist
	}
	path := filepath.Join(e.config.RunLogDir, id+".json")
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	entry, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !entry.Mode().IsRegular() || !os.SameFile(opened, entry) {
		return nil, os.ErrNotExist
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRunRecordBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRunRecordBytes {
		return nil, errors.New("shell run record exceeds read limit")
	}
	var record runRecord
	if err := json.Unmarshal(data, &record); err != nil || record.ID != id {
		return nil, errors.New("shell run record is invalid")
	}
	resource := struct {
		ID              string    `json:"id"`
		Hook            string    `json:"hook"`
		DeliveryID      string    `json:"delivery_id,omitempty"`
		StartedAt       time.Time `json:"started_at"`
		DurationMS      int64     `json:"duration_ms"`
		ExitCode        int       `json:"exit_code"`
		Failed          bool      `json:"failed"`
		Stdout          string    `json:"stdout,omitempty"`
		Stderr          string    `json:"stderr,omitempty"`
		Error           string    `json:"error,omitempty"`
		StdoutTruncated bool      `json:"stdout_truncated,omitempty"`
		StderrTruncated bool      `json:"stderr_truncated,omitempty"`
	}{
		ID: record.ID, Hook: record.Hook, DeliveryID: record.DeliveryID,
		StartedAt: record.StartedAt, DurationMS: record.DurationMS,
		ExitCode: record.ExitCode, Failed: record.ExitCode != 0 || record.Error != "",
	}
	if e.config.ExposeRunOutput {
		resource.Stdout, resource.Stderr, resource.Error = record.Stdout, record.Stderr, record.Error
		resource.StdoutTruncated, resource.StderrTruncated = record.StdoutTruncated, record.StderrTruncated
	}
	return json.Marshal(resource)
}

func (e *Engine) pruneRuns(now time.Time) error {
	if e.config.RunRetention == 0 {
		return nil
	}
	e.pruneMu.Lock()
	defer e.pruneMu.Unlock()
	if !e.lastPrune.IsZero() && now.Sub(e.lastPrune) < runPruneInterval {
		return nil
	}
	entries, err := os.ReadDir(e.config.RunLogDir)
	if err != nil {
		return err
	}
	cutoff := now.Add(-e.config.RunRetention)
	for _, entry := range entries {
		if !runRecordFilename(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(e.config.RunLogDir, entry.Name())); err != nil {
			return err
		}
	}
	e.lastPrune = now
	return nil
}

func runRecordFilename(name string) bool {
	if len(name) != 37 || !strings.HasSuffix(name, ".json") {
		return false
	}
	for _, digit := range name[:32] {
		if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') {
			return false
		}
	}
	return true
}

// OpenStream starts a hook whose stdout is raw response data. The caller owns
// SSE or chunked HTTP framing and must consume Chunks until Done closes.
func (e *Engine) OpenStream(ctx context.Context, source string, input automation.Input) (*automation.Stream, error) {
	if e == nil {
		return nil, errors.New("shell hook engine is not configured")
	}
	if e.config.OutputMode != ModeBuffered {
		return nil, errors.New("shell hook streaming requires buffered output mode")
	}
	releaseSlot, err := e.admit(ctx)
	if err != nil {
		return nil, err
	}
	release := true
	defer func() {
		if release {
			releaseSlot()
		}
	}()
	path, err := e.hookPath(source)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{"event": input.Event, "variables": input.Variables})
	if err != nil {
		return nil, fmt.Errorf("encode shell hook input: %w", err)
	}
	id, err := newRunID()
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, e.config.Timeout)
	command := e.hookCommand(runCtx, path)
	configureProcess(command)
	command.Stdin = bytes.NewReader(payload)
	command.Env = e.hookEnvironment()
	command.WaitDelay = 250 * time.Millisecond
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr := &limitedBuffer{limit: e.config.MaxOutputBytes}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start shell hook: %w", err)
	}
	chunks := make(chan []byte)
	done := make(chan error, 1)
	release = false
	go func() {
		defer cancel()
		defer releaseSlot()
		defer close(chunks)
		defer close(done)
		started := time.Now().UTC()
		record := runRecord{ID: id, Hook: source, DeliveryID: eventString(input.Event, "delivery_id"), StartedAt: started, ExitCode: -1}
		output := &limitedBuffer{limit: e.config.MaxOutputBytes}
		buffer := make([]byte, 4096)
		var readErr error
		for {
			count, err := stdout.Read(buffer)
			if count > 0 {
				piece := append([]byte(nil), buffer[:count]...)
				if _, limitErr := output.Write(piece); limitErr != nil {
					readErr = limitErr
					_ = killProcessGroup(command)
					break
				}
				select {
				case chunks <- piece:
				case <-runCtx.Done():
					readErr = runCtx.Err()
				}
				if readErr != nil {
					break
				}
			}
			if err != nil {
				if !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.EOF) {
					readErr = err
				}
				break
			}
		}
		waitErr := command.Wait()
		record.Stdout = output.String()
		record.Stderr = stderr.String()
		record.DurationMS = time.Since(started).Milliseconds()
		if command.ProcessState != nil {
			record.ExitCode = command.ProcessState.ExitCode()
		}
		runErr := readErr
		if runCtx.Err() != nil {
			runErr = runCtx.Err()
		} else if stderr.exceeded {
			runErr = fmt.Errorf("shell hook stderr exceeded %d bytes", e.config.MaxOutputBytes)
		} else if runErr == nil {
			runErr = waitErr
		}
		if runErr != nil {
			record.Error = runErr.Error()
		}
		if err := e.writeRecord(record); err != nil {
			runErr = fmt.Errorf("record shell hook run: %w", err)
		}
		done <- runErr
	}()
	return &automation.Stream{ID: id, Chunks: chunks, Done: done}, nil
}

func (e *Engine) admit(ctx context.Context) (func(), error) {
	select {
	case e.slots <- struct{}{}:
		return func() { <-e.slots }, nil
	default:
	}
	select {
	case e.queue <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	defer func() { <-e.queue }()
	waitCtx, cancel := context.WithTimeout(ctx, e.config.Timeout)
	defer cancel()
	select {
	case e.slots <- struct{}{}:
		return func() { <-e.slots }, nil
	case <-waitCtx.Done():
		return nil, fmt.Errorf("%w: queue wait ended: %v", ErrBusy, waitCtx.Err())
	}
}

func (e *Engine) hookPath(source string) (string, error) {
	if e == nil {
		return "", errors.New("shell hook engine is not configured")
	}
	key := strings.TrimSpace(source)
	if key == "" || !fs.ValidPath(key) || strings.Contains(key, "\\") {
		return "", errors.New("shell hook source must be a non-hidden relative path")
	}
	for _, segment := range strings.Split(key, "/") {
		if strings.HasPrefix(segment, ".") {
			return "", errors.New("shell hook source must be a non-hidden relative path")
		}
	}
	if e.config.Interpreter == InterpreterPowerShell && !strings.EqualFold(filepath.Ext(key), ".ps1") {
		return "", errors.New("PowerShell hook source must be a .ps1 file")
	}
	path, err := filepath.EvalSymlinks(filepath.Join(e.config.HooksDir, filepath.FromSlash(key)))
	if err != nil {
		return "", fmt.Errorf("resolve shell hook %q: %w", key, err)
	}
	relative, err := filepath.Rel(e.config.HooksDir, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("shell hook resolves outside the configured directory")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat shell hook %q: %w", key, err)
	}
	if !info.Mode().IsRegular() || (e.config.Interpreter == InterpreterDirect && info.Mode()&0o111 == 0) {
		return "", errors.New("shell hook must be an executable regular file")
	}
	return path, nil
}

func (e *Engine) hookCommand(ctx context.Context, path string) *exec.Cmd {
	if e.config.Interpreter == InterpreterPowerShell {
		return exec.CommandContext(ctx, e.config.PowerShellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", path)
	}
	return exec.CommandContext(ctx, path)
}

func (e *Engine) hookEnvironment() []string {
	path := "/usr/bin:/bin"
	env := []string{"GATEWAY_AUTOMATION_LANGUAGE=shell"}
	if runtime.GOOS == "windows" && e.config.Interpreter == InterpreterPowerShell {
		path = filepath.Dir(e.config.PowerShellPath)
		for _, name := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
			if value := os.Getenv(name); value != "" {
				env = append(env, name+"="+value)
			}
		}
		if root := os.Getenv("SystemRoot"); root != "" {
			path += string(os.PathListSeparator) + filepath.Join(root, "System32")
		}
	}
	return append(env, "PATH="+path)
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return duration, nil
}

func boolEnv(name string) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", name)
	}
	return parsed, nil
}

func int64Env(name string, fallback int64) (int64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func intEnv(name string, fallback int) (int, error) {
	value, err := int64Env(name, int64(fallback))
	if err != nil || value > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("%s must be a positive integer within platform range", name)
	}
	return int(value), nil
}

func nonnegativeIntEnv(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || parsed > int64(^uint(0)>>1) {
		return 0, fmt.Errorf("%s must be a nonnegative integer within platform range", name)
	}
	return int(parsed), nil
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int64
	exceeded bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	remaining := b.limit - int64(b.buffer.Len())
	if remaining <= 0 || int64(len(value)) > remaining {
		if remaining > 0 {
			_, _ = b.buffer.Write(value[:remaining])
		}
		b.exceeded = true
		return len(value), errors.New("output limit exceeded")
	}
	return b.buffer.Write(value)
}

func (b *limitedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *limitedBuffer) String() string { return b.buffer.String() }

var _ automation.Engine = (*Engine)(nil)
