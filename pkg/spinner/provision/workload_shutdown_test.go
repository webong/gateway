package provision

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webong/workload"
)

type uncertainExecution struct {
	allowStop atomic.Bool
	done      chan struct{}
	once      sync.Once
}

func (*uncertainExecution) Endpoints() []workload.Endpoint {
	return []workload.Endpoint{{Name: "default", Host: "127.0.0.1", Port: 8080}}
}
func (*uncertainExecution) PID() int                { return 123 }
func (p *uncertainExecution) Done() <-chan struct{} { return p.done }
func (*uncertainExecution) Err() error              { return nil }
func (p *uncertainExecution) Stop(context.Context) error {
	if !p.allowStop.Load() {
		return errors.New("termination unconfirmed")
	}
	p.once.Do(func() { close(p.done) })
	return nil
}

type uncertainStarter struct{ process *uncertainExecution }

func (s uncertainStarter) Start(context.Context, workload.Spec) (workload.Process, error) {
	return s.process, nil
}

type shutdownControl struct {
	spec  workload.Spec
	ready chan struct{}
	once  sync.Once
}

func (c *shutdownControl) Assignments(context.Context, string, []string) ([]workload.Spec, string, error) {
	return []workload.Spec{c.spec}, "node-a", nil
}
func (c *shutdownControl) Report(_ context.Context, _, _ string, r workload.InstanceReport) error {
	if r.State == "running" {
		c.once.Do(func() { close(c.ready) })
	}
	return nil
}

type shutdownRoutes struct{ withdrawn atomic.Bool }

func (*shutdownRoutes) Add(workload.Spec, string, []workload.Endpoint) {}
func (r *shutdownRoutes) Remove(string, string)                        { r.withdrawn.Store(true) }

func TestRuntimeRetainsOwnershipUntilWorkloadConfirmsTermination(t *testing.T) {
	spec, err := executionSpec(validServerSpec(), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	process := &uncertainExecution{done: make(chan struct{})}
	control := &shutdownControl{spec: spec, ready: make(chan struct{})}
	routes := &shutdownRoutes{}
	core, err := workload.NewReconciler(workload.ReconcilerConfig{NodeID: "node-a", Drivers: []string{DriverLocal}, CoordinationLease: time.Minute, PollInterval: time.Second, StartTimeout: time.Second, StopTimeout: time.Second}, control, uncertainStarter{process}, routes, &fakeLogger{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	owner := &countingOwner{}
	releases := 0
	runtime := &Runtime{cancel: cancel, done: done, core: core, owner: owner, stopped: make(chan struct{}), release: func(context.Context) error { releases++; return nil }}
	go func() { defer close(done); runtime.runErr = core.Run(ctx) }()
	defer func() { process.allowStop.Store(true); _ = runtime.StopContext(context.Background()) }()
	select {
	case <-control.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("execution did not start")
	}
	if err := runtime.StopContext(t.Context()); err == nil {
		t.Fatal("unconfirmed termination hidden")
	}
	if releases != 0 || owner.closed != 0 {
		t.Fatalf("released uncertain ownership: releases=%d closes=%d", releases, owner.closed)
	}
	if !routes.withdrawn.Load() {
		t.Fatal("uncertain resource kept its route")
	}
	select {
	case <-process.done:
		t.Fatal("failed stop falsely confirmed termination")
	default:
	}
	process.allowStop.Store(true)
	// Cleanup succeeds on retry; the original runtime failure still reaches the caller.
	if err := runtime.StopContext(t.Context()); err == nil {
		t.Fatal("original terminal failure hidden after cleanup retry")
	}
	if releases != 1 || owner.closed != 1 {
		t.Fatalf("confirmed cleanup did not release once: releases=%d closes=%d", releases, owner.closed)
	}
}
