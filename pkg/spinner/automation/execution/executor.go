// Package execution selects the local execution mechanism used by Spinner
// while preserving the driver automation.Executor contract for callers.
package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/webong/gateway/arranger/automation"
	"github.com/webong/gateway/pkg/spinner/automation/actors"
)

type Mode = automation.ExecutorMode

const (
	ModeDirect    = automation.ExecutorDirect
	ModeGoroutine = automation.ExecutorGoroutine
	ModeErgo      = automation.ExecutorErgo
)

type Options struct {
	Mode      Mode
	Workers   int
	QueueSize int
	Timeout   time.Duration
}

// Executor is the managed Spinner adapter returned for every execution mode.
// Callers continue to depend only on automation.Executor.
type Executor struct {
	delegate    automation.Executor
	executors   map[Mode]automation.Executor
	defaultMode Mode
	options     Options
	mu          sync.Mutex
	closed      bool
}

func New(delegate automation.Executor, options Options) (*Executor, error) {
	if delegate == nil {
		return nil, errors.New("automation execution delegate is required")
	}
	if options.Workers < 1 || options.QueueSize < 0 || options.Timeout <= 0 {
		return nil, errors.New("automation workers and timeout must be positive and queue size must be non-negative")
	}
	mode := Mode(strings.ToLower(strings.TrimSpace(string(options.Mode))))
	if mode == "" {
		mode = ModeErgo
	}

	if !automation.ValidExecutorMode(mode) || mode == automation.ExecutorDefault {
		return nil, fmt.Errorf("unsupported automation execution mode %q", mode)
	}

	executor := &Executor{
		delegate:    delegate,
		executors:   map[Mode]automation.Executor{ModeDirect: delegate},
		defaultMode: mode,
		options:     options,
	}
	if _, err := executor.ExecutorFor(mode); err != nil {
		return nil, err
	}
	return executor, nil
}

func (e *Executor) Mode() Mode {
	if e == nil {
		return ""
	}
	return e.defaultMode
}

// ExecutorFor resolves one request's choice. An empty choice uses the default
// selected by GATEWAY_AUTOMATION_EXECUTOR at Spinner composition time.
func (e *Executor) ExecutorFor(mode Mode) (automation.Executor, error) {
	if e == nil {
		return nil, fmt.Errorf("%w: executor is unavailable", automation.ErrBusy)
	}
	mode = Mode(strings.ToLower(strings.TrimSpace(string(mode))))
	if mode == automation.ExecutorDefault {
		mode = e.defaultMode
	}
	if !automation.ValidExecutorMode(mode) {
		return nil, fmt.Errorf("unsupported automation execution mode %q", mode)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, fmt.Errorf("%w: executor is stopped", automation.ErrBusy)
	}
	if executor, ok := e.executors[mode]; ok {
		return executor, nil
	}

	var executor automation.Executor
	var err error
	switch mode {
	case ModeGoroutine:
		executor, err = newGoroutineExecutor(e.delegate, e.options)
	case ModeErgo:
		executor, err = actors.New(e.delegate, actors.Options{
			Workers: e.options.Workers, QueueSize: e.options.QueueSize, Timeout: e.options.Timeout,
		})
	default:
		return nil, fmt.Errorf("unsupported automation execution mode %q", mode)
	}
	if err != nil {
		return nil, err
	}
	e.executors[mode] = executor
	return executor, nil
}

func (e *Executor) Supports(language automation.Language) bool {
	return e != nil && e.delegate.Supports(language)
}

func (e *Executor) Languages() []automation.Language {
	if e == nil {
		return []automation.Language{}
	}
	return automation.Languages(e.delegate)
}

func (e *Executor) Execute(ctx context.Context, script automation.Script, input automation.Input) (automation.Outcome, error) {
	executor, err := e.ExecutorFor(script.Executor)
	if err != nil {
		return automation.Outcome{}, err
	}
	return executor.Execute(ctx, script, input)
}

func (e *Executor) OpenStream(ctx context.Context, script automation.Script, input automation.Input) (*automation.Stream, error) {
	executor, err := e.ExecutorFor(script.Executor)
	if err != nil {
		return nil, err
	}
	streamer, ok := executor.(automation.StreamingExecutor)
	if !ok {
		return nil, errors.New("automation streaming is unavailable")
	}
	return streamer.OpenStream(ctx, script, input)
}

func (e *Executor) Close() {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	goroutineExecutor := e.executors[ModeGoroutine]
	ergoExecutor := e.executors[ModeErgo]
	e.mu.Unlock()

	if executor, ok := goroutineExecutor.(interface{ Close() }); ok {
		executor.Close()
	}
	if executor, ok := ergoExecutor.(interface{ Close() }); ok {
		executor.Close()
	}
}

var _ automation.Executor = (*Executor)(nil)
var _ automation.LanguageCatalog = (*Executor)(nil)
var _ automation.StreamingExecutor = (*Executor)(nil)
