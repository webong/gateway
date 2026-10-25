package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webong/gateway/arranger/automation"
)

// goroutineExecutor bounds in-process automation work without an actor runtime.
type goroutineExecutor struct {
	delegate automation.Executor
	jobs     chan goroutineExecution
	timeout  time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	closed   atomic.Bool
	workers  sync.WaitGroup
}

type goroutineExecution struct {
	ctx     context.Context
	script  automation.Script
	input   automation.Input
	result  chan<- goroutineResult
	started chan struct{}
}

type goroutineResult struct {
	outcome automation.Outcome
	err     error
}

func newGoroutineExecutor(delegate automation.Executor, options Options) (*goroutineExecutor, error) {
	if options.Workers < 1 || options.QueueSize < 0 || options.Timeout <= 0 {
		return nil, errors.New("automation goroutine workers and timeout must be positive and queue size must be non-negative")
	}
	ctx, cancel := context.WithCancel(context.Background())
	executor := &goroutineExecutor{
		delegate: delegate,
		jobs:     make(chan goroutineExecution, options.QueueSize),
		timeout:  options.Timeout,
		ctx:      ctx,
		cancel:   cancel,
	}
	executor.workers.Add(options.Workers)
	for range options.Workers {
		go executor.work()
	}
	return executor, nil
}

func (e *goroutineExecutor) Supports(language automation.Language) bool {
	return e != nil && e.delegate.Supports(language)
}

func (e *goroutineExecutor) Languages() []automation.Language {
	return automation.Languages(e.delegate)
}

func (e *goroutineExecutor) Execute(ctx context.Context, script automation.Script, input automation.Input) (automation.Outcome, error) {
	if e == nil || e.closed.Load() {
		return automation.Outcome{}, fmt.Errorf("%w: goroutine pool is stopped", automation.ErrBusy)
	}
	runCtx, cancel := context.WithTimeout(ctx, e.timeout)
	stopOnClose := context.AfterFunc(e.ctx, cancel)
	defer func() {
		stopOnClose()
		cancel()
	}()

	result := make(chan goroutineResult, 1)
	started := make(chan struct{})
	job := goroutineExecution{ctx: runCtx, script: script, input: input, result: result, started: started}
	select {
	case e.jobs <- job:
	case <-runCtx.Done():
		return automation.Outcome{}, queueError(ctx, e.timeout)
	case <-e.ctx.Done():
		return automation.Outcome{}, fmt.Errorf("%w: goroutine pool is stopped", automation.ErrBusy)
	}

	select {
	case completed := <-result:
		return completed.outcome, completed.err
	case <-runCtx.Done():
		select {
		case <-started:
			return automation.Outcome{}, runCtx.Err()
		default:
			return automation.Outcome{}, queueError(ctx, e.timeout)
		}
	case <-e.ctx.Done():
		return automation.Outcome{}, fmt.Errorf("%w: goroutine pool is stopped", automation.ErrBusy)
	}
}

func (e *goroutineExecutor) OpenStream(ctx context.Context, script automation.Script, input automation.Input) (*automation.Stream, error) {
	streamer, ok := e.delegate.(automation.StreamingExecutor)
	if !ok {
		return nil, errors.New("automation streaming is unavailable")
	}
	return streamer.OpenStream(ctx, script, input)
}

func (e *goroutineExecutor) Close() {
	if e != nil && e.closed.CompareAndSwap(false, true) {
		e.cancel()
		e.workers.Wait()
	}
}

func (e *goroutineExecutor) work() {
	defer e.workers.Done()
	for {
		select {
		case <-e.ctx.Done():
			return
		case job := <-e.jobs:
			close(job.started)
			if err := job.ctx.Err(); err != nil {
				job.result <- goroutineResult{err: err}
				continue
			}
			outcome, err := e.delegate.Execute(job.ctx, job.script, job.input)
			job.result <- goroutineResult{outcome: outcome, err: err}
		}
	}
}

func queueError(ctx context.Context, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w: queue wait exceeded %s", automation.ErrBusy, timeout)
}

var _ automation.Executor = (*goroutineExecutor)(nil)
var _ automation.LanguageCatalog = (*goroutineExecutor)(nil)
var _ automation.StreamingExecutor = (*goroutineExecutor)(nil)
