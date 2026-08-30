package provisioning

import (
	"context"
	"fmt"
	"sort"
)

type Driver interface {
	Start(context.Context, ServerSpec, string, Workload) (Process, error)
}

type Starter interface {
	Start(context.Context, ServerSpec, string) (Process, error)
}

type Process interface {
	PID() int
	Host() string
	Port() int
	Done() <-chan struct{}
	Err() error
	Stop(context.Context) error
}

type DriverRegistry struct {
	drivers   map[string]Driver
	workloads map[string]Workload
}

type driverPreparer interface {
	Prepare(context.Context) error
}

func NewDriverRegistry(drivers map[string]Driver, workloads []Workload) (*DriverRegistry, error) {
	configured := make(map[string]Driver, len(drivers))
	for name, driver := range drivers {
		if driver == nil {
			return nil, fmt.Errorf("provisioning %s driver is nil", name)
		}
		configured[name] = driver
	}
	if len(configured) == 0 {
		return nil, fmt.Errorf("at least one provisioning runtime driver is required")
	}
	configuredWorkloads := make(map[string]Workload, len(workloads))
	for _, workload := range workloads {
		if workload == nil || workload.Type() == "" {
			return nil, fmt.Errorf("provisioning workload type is required")
		}
		if _, exists := configuredWorkloads[workload.Type()]; exists {
			return nil, fmt.Errorf("provisioning workload %q is registered twice", workload.Type())
		}
		configuredWorkloads[workload.Type()] = workload
	}
	if len(configuredWorkloads) == 0 {
		return nil, fmt.Errorf("at least one provisioning workload is required")
	}

	return &DriverRegistry{drivers: configured, workloads: configuredWorkloads}, nil
}

func (r *DriverRegistry) Start(ctx context.Context, spec ServerSpec, instanceID string) (Process, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	driver, exists := r.drivers[spec.Driver]
	if !exists {
		return nil, fmt.Errorf("provisioning %s driver is not configured on this Gateway node", spec.Driver)
	}
	workload, exists := r.workloads[spec.Type]
	if !exists {
		return nil, fmt.Errorf("provisioning workload %q is not configured on this Gateway node", spec.Type)
	}
	if err := workload.Validate(spec); err != nil {
		return nil, err
	}

	return driver.Start(ctx, spec, instanceID, workload)
}

func (r *DriverRegistry) Prepare(ctx context.Context) error {
	names := make([]string, 0, len(r.drivers))
	for name := range r.drivers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		preparer, ok := r.drivers[name].(driverPreparer)
		if !ok {
			continue
		}
		if err := preparer.Prepare(ctx); err != nil {
			return fmt.Errorf("prepare provisioning %s driver: %w", name, err)
		}
	}

	return nil
}

var _ Starter = (*DriverRegistry)(nil)
