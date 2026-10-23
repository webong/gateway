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

	"github.com/webong/gateway/driver/automation"
	"github.com/webong/gateway/pkg/spinner/automation/actors"
)

type Mode string

const (
	ModeDirect    Mode = "direct"
	ModeGoroutine Mode = "goroutine"
	ModeErgo      Mode = "ergo"
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
	delegate automation.Executor
	mode     Mode
	close    func()
	once     sync.Once
}

func New(delegate automation.Executor, options Options) (*Executor, error) {
	if delegate == nil {
		return nil, errors.New("automation execution delegate is required")
	}
	mode := Mode(strings.ToLower(strings.TrimSpace(string(options.Mode))))
	if mode == "" {
		mode = ModeErgo
	}

	var selected automation.Executor
	var closeSelected func()
	switch mode {
	case ModeDirect:
		selected = delegate
		closeSelected = func() {}
	case ModeGoroutine:
		executor, err := newGoroutineExecutor(delegate, options)
		if err != nil {
			return nil, err
		}
		selected = executor
		closeSelected = executor.Close
	case ModeErgo:
		executor, err := actors.New(delegate, actors.Options{
			Workers: options.Workers, QueueSize: options.QueueSize, Timeout: options.Timeout,
		})
		if err != nil {
			return nil, err
		}
		selected = executor
		closeSelected = executor.Close
	default:
		return nil, fmt.Errorf("unsupported automation execution mode %q", mode)
	}

	return &Executor{delegate: selected, mode: mode, close: closeSelected}, nil
}

func (e *Executor) Mode() Mode {
	if e == nil {
		return ""
	}
	return e.mode
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
	if e == nil || e.delegate == nil {
		return automation.Outcome{}, fmt.Errorf("%w: executor is unavailable", automation.ErrBusy)
	}
	return e.delegate.Execute(ctx, script, input)
}

func (e *Executor) OpenStream(ctx context.Context, script automation.Script, input automation.Input) (*automation.Stream, error) {
	if e == nil || e.delegate == nil {
		return nil, errors.New("automation streaming is unavailable")
	}
	streamer, ok := e.delegate.(automation.StreamingExecutor)
	if !ok {
		return nil, errors.New("automation streaming is unavailable")
	}
	return streamer.OpenStream(ctx, script, input)
}

func (e *Executor) Close() {
	if e != nil {
		e.once.Do(e.close)
	}
}

var _ automation.Executor = (*Executor)(nil)
var _ automation.LanguageCatalog = (*Executor)(nil)
var _ automation.StreamingExecutor = (*Executor)(nil)
