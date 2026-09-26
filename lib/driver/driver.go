package driver

import "context"

// Driver starts a workload in a runtime such as a local process or container.
// A successful Start returns a live process handle owned by the caller.
type Driver interface {
	Start(ctx context.Context, spec ServerSpec, instanceID string, workload Workload) (Process, error)
}

// Process describes a running workload. Done closes when it exits; Err reports
// its exit error after Done closes. Stop must honor the supplied context.
type Process interface {
	PID() int
	Host() string
	Port() int
	Done() <-chan struct{}
	Err() error
	Stop(context.Context) error
}
