package schedules

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"ergo.services/ergo"
	"ergo.services/ergo/act"
	"ergo.services/ergo/gen"
)

const scheduleActorCount = 5 // Planner claims at most five runs per poll.

var scheduleNodeSequence atomic.Uint64

type actorPool struct {
	node    gen.Node
	pid     gen.PID
	runtime Runtime
}

func newActorPool(runtime Runtime) (*actorPool, error) {
	name := gen.Atom(fmt.Sprintf("gateway-schedules-%d-%d@localhost", os.Getpid(), scheduleNodeSequence.Add(1)))
	node, err := ergo.StartNode(name, gen.NodeOptions{
		Network: gen.NetworkOptions{Mode: gen.NetworkModeDisabled},
	})
	if err != nil {
		return nil, err
	}
	pool := &actorPool{node: node, runtime: runtime}
	pid, err := node.Spawn(func() gen.ProcessBehavior {
		return &schedulePool{runtime: runtime}
	}, gen.ProcessOptions{MailboxSize: scheduleActorCount})
	if err != nil {
		node.Stop()
		return nil, err
	}
	pool.pid = pid
	return pool, nil
}

// Stop does not let a stuck guest script turn an Ergo shutdown timeout into
// termination of the entire Gateway process.
func (p *actorPool) Stop() { p.node.StopForce() }

func (p *actorPool) Dispatch(ctx context.Context, run Run) {
	// Keep actor work inside the Planner claim's two-minute lease. A timeout
	// leaves the run claimed so the normal lease recovery path can retry it.
	runCtx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	task := &scheduleTask{ctx: runCtx, run: run, done: make(chan struct{})}
	if err := p.node.Send(p.pid, task); err != nil {
		p.runtime.log("send schedule run %s to actor pool: %v", run.ID, err)
		return
	}
	select {
	case <-task.done:
		if !task.finished {
			p.runtime.log("schedule actor stopped before completing run %s; lease recovery will retry it", run.ID)
		}
	case <-runCtx.Done():
		p.runtime.log("schedule actor timed out on run %s; lease recovery will retry it", run.ID)
	}
}

type scheduleTask struct {
	ctx      context.Context
	run      Run
	done     chan struct{}
	finished bool
}

type schedulePool struct {
	act.Pool
	runtime Runtime
}

func (p *schedulePool) Init(args ...any) (act.PoolOptions, error) {
	return act.PoolOptions{
		PoolSize: scheduleActorCount, WorkerMailboxSize: 1,
		WorkerFactory: func() gen.ProcessBehavior { return &scheduleWorker{runtime: p.runtime} },
	}, nil
}

type scheduleWorker struct {
	act.Actor
	runtime Runtime
}

func (w *scheduleWorker) HandleMessage(_ gen.PID, message any) error {
	task, ok := message.(*scheduleTask)
	if !ok {
		return nil
	}
	defer close(task.done)
	w.runtime.execute(task.ctx, task.run)
	task.finished = true
	return nil
}
