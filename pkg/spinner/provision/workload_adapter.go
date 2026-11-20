package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/webong/gateway/arranger/integration"
	"github.com/webong/workload"
)

// Workload sees execution identity and opaque configuration. Gateway retains
// its routing/authoring document and never asks Workload to place replicas.
func executionSpec(spec ServerSpec, deadline time.Time) (workload.Spec, error) {
	if err := spec.Validate(); err != nil {
		return workload.Spec{}, err
	}
	configuration, err := json.Marshal(spec)
	if err != nil {
		return workload.Spec{}, err
	}
	return workload.Spec{AssignmentID: spec.AssignmentID, TargetID: spec.TargetID, ID: spec.ID,
		Type: spec.Type, Driver: spec.Driver, DesiredState: spec.DesiredState, Revision: spec.Revision,
		Configuration: configuration, LeaseUntil: deadline}, nil
}

func gatewaySpec(spec workload.Spec) (ServerSpec, error) {
	var gateway ServerSpec
	if err := json.Unmarshal(spec.Configuration, &gateway); err != nil {
		return gateway, err
	}
	if err := gateway.Validate(); err != nil {
		return gateway, err
	}
	if gateway.AssignmentID != spec.AssignmentID || gateway.TargetID != spec.TargetID || gateway.ID != spec.ID ||
		gateway.Type != spec.Type || gateway.Driver != spec.Driver || gateway.Revision != spec.Revision {
		return gateway, fmt.Errorf("execution envelope does not match Gateway assignment")
	}
	return gateway, nil
}

type definitionAdapter struct{ source Provisioner }

func (d definitionAdapter) Type() string                { return d.source.Type() }
func (d definitionAdapter) Supports(driver string) bool { return d.source.Supports(driver) }
func (d definitionAdapter) Validate(ctx context.Context, spec workload.Spec) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	gateway, err := gatewaySpec(spec)
	if err != nil {
		return err
	}
	if !d.source.Supports(spec.Driver) {
		return fmt.Errorf("integration %s does not support %s", spec.Type, spec.Driver)
	}
	if contextual, ok := d.source.(interface {
		ValidateContext(context.Context, ServerSpec) error
	}); ok {
		return contextual.ValidateContext(ctx, gateway)
	}
	return d.source.Validate(gateway)
}
func (d definitionAdapter) Prepare(ctx context.Context, spec workload.Spec, placement workload.Placement) (workload.LaunchSpec, error) {
	if err := ctx.Err(); err != nil {
		return workload.LaunchSpec{}, err
	}
	gateway, err := gatewaySpec(spec)
	if err != nil {
		return workload.LaunchSpec{}, err
	}
	var launch LaunchSpec
	if placed, ok := d.source.(interface {
		LaunchPlacementContext(context.Context, ServerSpec, integration.Placement) (LaunchSpec, error)
	}); ok {
		launch, err = placed.LaunchPlacementContext(ctx, gateway, integration.Placement{Driver: placement.Driver, BindHost: placement.BindHost, Ports: placement.Ports})
	} else if placed, ok := d.source.(integration.PlacementProvisioner); ok {
		launch, err = placed.LaunchPlacement(gateway, integration.Placement{Driver: placement.Driver, BindHost: placement.BindHost, Ports: placement.Ports})
	} else if contextual, ok := d.source.(interface {
		LaunchContext(context.Context, ServerSpec, string, string, int) (LaunchSpec, error)
	}); ok {
		launch, err = contextual.LaunchContext(ctx, gateway, placement.Driver, placement.BindHost, placement.Ports["default"])
	} else {
		launch, err = d.source.Launch(gateway, placement.Driver, placement.BindHost, placement.Ports["default"])
	}
	if err != nil {
		return workload.LaunchSpec{}, err
	}
	endpoints := make([]workload.EndpointSpec, 0, len(launch.Endpoints))
	hasDefault := false
	for _, endpoint := range launch.Endpoints {
		endpoints = append(endpoints, workload.EndpointSpec{Name: endpoint.Name, Port: endpoint.Port})
		hasDefault = hasDefault || endpoint.Name == "default"
	}
	if len(endpoints) > 1 && !hasDefault {
		return workload.LaunchSpec{}, fmt.Errorf("multiple workload endpoints require the default route endpoint")
	}
	return workload.LaunchSpec{Endpoints: endpoints, Limits: workload.ResourceLimits{MemoryBytes: launch.Limits.MemoryBytes, MilliCPU: launch.Limits.MilliCPU, PIDs: launch.Limits.PIDs}, Image: launch.Image, Executable: launch.Executable, Arguments: launch.Arguments,
		Environment: launch.Environment, WorkingDirectory: launch.WorkingDirectory, Port: launch.Port,
		ContainerName: launch.ContainerName}, nil
}

type workloadControl struct {
	control  *HTTPControlPlane
	router   *Router
	maxLease time.Duration
}

func (c *workloadControl) Assignments(ctx context.Context, node string, drivers []string) ([]workload.Spec, string, error) {
	// HTTPControlPlane stamps deadlines from request-start plus the validated
	// server grant, conservatively spending the complete round-trip budget.
	snapshot, leader, err := c.control.executionAssignments(ctx, node, drivers, c.maxLease)
	if err != nil {
		return nil, "", err
	}
	result := make([]workload.Spec, 0, len(snapshot.specs))
	seenAssignments := make(map[string]struct{}, len(snapshot.specs))
	for _, spec := range snapshot.specs {
		assignment, err := executionSpec(spec, snapshot.deadline)
		if err != nil {
			return nil, "", err
		}
		if _, duplicate := seenAssignments[spec.AssignmentID]; duplicate {
			return nil, "", fmt.Errorf("duplicate assignment %s", spec.AssignmentID)
		}
		seenAssignments[spec.AssignmentID] = struct{}{}
		result = append(result, assignment)
	}
	return result, leader, nil
}
func (c *workloadControl) Report(ctx context.Context, id, assignment string, report workload.InstanceReport) error {
	state := report.State
	switch state {
	case "stop-failed", "unhealthy":
		state = "stopping"
	}
	observed := InstanceReport{NodeID: report.NodeID, Runtime: report.Runtime, PID: report.PID, State: state, Revision: report.Revision, Error: report.Error, Endpoints: plannerEndpoints(report.Endpoints)}
	endpoint, err := selectedEndpoint(report.Endpoints)
	if err != nil {
		return err
	}
	if endpoint != nil {
		observed.Host = endpoint.Host
		observed.Port = endpoint.Port
	}
	return c.control.Report(ctx, id, assignment, observed)
}
func selectedEndpoint(endpoints []workload.Endpoint) (*workload.Endpoint, error) {
	for _, e := range endpoints {
		if e.Name == "default" {
			return &e, nil
		}
	}
	if len(endpoints) == 1 {
		return &endpoints[0], nil
	}
	if len(endpoints) == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("multiple workload endpoints require the default route endpoint")
}
func (c *workloadControl) Add(spec workload.Spec, id string, endpoints []workload.Endpoint) {
	gateway, err := gatewaySpec(spec)
	if err != nil {
		return
	}
	endpoint, err := selectedEndpoint(endpoints)
	if err != nil || endpoint == nil {
		return
	}
	c.router.Add(gateway, id, endpoint.Host, endpoint.Port)
}
func (c *workloadControl) Remove(id, assignment string) { c.router.Remove(id, assignment) }

func plannerEndpoints(endpoints []workload.Endpoint) []InstanceEndpoint {
	result := make([]InstanceEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		result = append(result, InstanceEndpoint{Name: endpoint.Name, Host: endpoint.Host, Port: endpoint.Port})
	}
	return result
}
