package provision

import (
	"context"
	"fmt"
	"sort"
)

type Starter interface {
	Start(context.Context, ServerSpec, string) (Process, error)
}

type DriverRegistry struct {
	drivers   map[string]Driver
	workloads map[string]Provisioner
}

type driverPreparer interface {
	PrepareDesired(context.Context, map[string]ServerSpec) error
}

func NewDriverRegistry(drivers map[string]Driver, workloads []Provisioner) (*DriverRegistry, error) {
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
	configuredWorkloads := make(map[string]Provisioner, len(workloads))
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

// PrepareDesired removes stale owned resources only after the control plane has
// supplied a successful assignment snapshot. Desired resources remain in place
// for the driver's Start method to adopt.
func (r *DriverRegistry) PrepareDesired(ctx context.Context, specifications []ServerSpec) error {
	desired := make(map[string]map[string]ServerSpec, len(r.drivers))
	for _, spec := range specifications {
		if err := spec.Validate(); err != nil {
			return err
		}
		if _, exists := r.drivers[spec.Driver]; !exists {
			return fmt.Errorf("provisioning %s driver is not configured on this Gateway node", spec.Driver)
		}
		if desired[spec.Driver] == nil {
			desired[spec.Driver] = make(map[string]ServerSpec)
		}
		desired[spec.Driver][spec.AssignmentID] = spec
	}
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
		if err := preparer.PrepareDesired(ctx, desired[name]); err != nil {
			return fmt.Errorf("prepare provisioning %s driver: %w", name, err)
		}
	}

	return nil
}

var _ Starter = (*DriverRegistry)(nil)
