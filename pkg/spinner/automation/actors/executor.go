// Package actors runs bounded automation work on a local Ergo actor pool.
package actors

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
	"github.com/webong/gateway/arranger/automation"
)

type Options struct {
	Workers   int
	QueueSize int
	Timeout   time.Duration
}

type Executor struct {
	delegate automation.Executor
	node     gen.Node
	pool     gen.PID
	capacity chan struct{}
	timeout  time.Duration
	closed   atomic.Bool
}

var nodeSequence atomic.Uint64

func New(delegate automation.Executor, options Options) (*Executor, error) {
	if delegate == nil {
		return nil, errors.New("automation actor delegate is required")
	}
	if options.Workers < 1 || options.QueueSize < 0 || options.Timeout <= 0 {
		return nil, errors.New("automation actor workers and timeout must be positive and queue size must be non-negative")
	}
	name := gen.Atom(fmt.Sprintf("gateway-automation-%d-%d@localhost", os.Getpid(), nodeSequence.Add(1)))
	node, err := ergo.StartNode(name, gen.NodeOptions{Network: gen.NetworkOptions{Mode: gen.NetworkModeDisabled}})
	if err != nil {
		return nil, err
	}
	executor := &Executor{
		delegate: delegate,
		node:     node,
		capacity: make(chan struct{}, options.Workers+options.QueueSize),
		timeout:  options.Timeout,
	}
	mailboxPerWorker := int64((options.QueueSize+options.Workers-1)/options.Workers + 1)
	pid, err := node.Spawn(func() gen.ProcessBehavior {
		return &pool{delegate: delegate, workers: int64(options.Workers), mailboxSize: mailboxPerWorker}
	}, gen.ProcessOptions{MailboxSize: int64(options.Workers + options.QueueSize)})
	if err != nil {
		node.StopForce()
		return nil, err
	}
	executor.pool = pid
	return executor, nil
}

func (e *Executor) Supports(language automation.Language) bool {
	return e != nil && e.delegate.Supports(language)
}

func (e *Executor) Languages() []automation.Language {
	return automation.Languages(e.delegate)
}

func (e *Executor) Execute(ctx context.Context, script automation.Script, input automation.Input) (automation.Outcome, error) {
	if e == nil || e.closed.Load() {
		return automation.Outcome{}, fmt.Errorf("%w: actor pool is stopped", automation.ErrBusy)
	}
	runCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	select {
	case e.capacity <- struct{}{}:
	case <-runCtx.Done():
		if ctx.Err() != nil {
			return automation.Outcome{}, ctx.Err()
		}
		return automation.Outcome{}, fmt.Errorf("%w: queue wait exceeded %s", automation.ErrBusy, e.timeout)
	}

	result := make(chan executionResult, 1)
	started := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { <-e.capacity }) }
	if err := e.node.Send(e.pool, execution{ctx: runCtx, script: script, input: input, result: result, started: started, release: release}); err != nil {
		release()
		return automation.Outcome{}, fmt.Errorf("%w: actor dispatch failed: %v", automation.ErrBusy, err)
	}
	select {
	case completed := <-result:
		return completed.outcome, completed.err
	case <-runCtx.Done():
		select {
		case <-started:
			return automation.Outcome{}, runCtx.Err()
		default:
			if ctx.Err() != nil {
				return automation.Outcome{}, ctx.Err()
			}
			return automation.Outcome{}, fmt.Errorf("%w: queue wait exceeded %s", automation.ErrBusy, e.timeout)
		}
	}
}

// OpenStream preserves Shell's live streaming path. A stream cannot wait in a
// mailbox because its response begins before execution completes.
func (e *Executor) OpenStream(ctx context.Context, script automation.Script, input automation.Input) (*automation.Stream, error) {
	streamer, ok := e.delegate.(automation.StreamingExecutor)
	if !ok {
		return nil, errors.New("automation streaming is unavailable")
	}
	return streamer.OpenStream(ctx, script, input)
}

func (e *Executor) Close() {
	if e != nil && e.closed.CompareAndSwap(false, true) {
		e.node.StopForce()
	}
}

type execution struct {
	ctx     context.Context
	script  automation.Script
	input   automation.Input
	result  chan<- executionResult
	started chan struct{}
	release func()
}

type executionResult struct {
	outcome automation.Outcome
	err     error
}

type pool struct {
	act.Pool
	delegate    automation.Executor
	workers     int64
	mailboxSize int64
}

func (p *pool) Init(...any) (act.PoolOptions, error) {
	return act.PoolOptions{
		PoolSize: p.workers, WorkerMailboxSize: p.mailboxSize,
		WorkerFactory: func() gen.ProcessBehavior { return &worker{delegate: p.delegate} },
	}, nil
}

type worker struct {
	act.Actor
	delegate automation.Executor
}

func (w *worker) HandleMessage(_ gen.PID, message any) error {
	job, ok := message.(execution)
	if !ok {
		return nil
	}
	defer job.release()
	close(job.started)
	if err := job.ctx.Err(); err != nil {
		job.result <- executionResult{err: err}
		return nil
	}
	outcome, err := w.delegate.Execute(job.ctx, job.script, job.input)
	job.result <- executionResult{outcome: outcome, err: err}
	return nil
}

var _ automation.Executor = (*Executor)(nil)
var _ automation.LanguageCatalog = (*Executor)(nil)
var _ automation.StreamingExecutor = (*Executor)(nil)
