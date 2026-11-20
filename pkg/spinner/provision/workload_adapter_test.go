package provision

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/webong/gateway/arranger/integration"
	"github.com/webong/workload"
)

type placementDefinition struct {
	got    integration.Placement
	launch LaunchSpec
}

func (*placementDefinition) Type() string              { return "test" }
func (*placementDefinition) Supports(string) bool      { return true }
func (*placementDefinition) Validate(ServerSpec) error { return nil }
func (*placementDefinition) Launch(ServerSpec, string, string, int) (LaunchSpec, error) {
	panic("placement path required")
}
func (d *placementDefinition) LaunchPlacement(_ ServerSpec, p integration.Placement) (LaunchSpec, error) {
	d.got = p
	return d.launch, nil
}

func TestWorkloadAdapterPreservesAuthoringAndReusableEndpoints(t *testing.T) {
	definition := &placementDefinition{launch: LaunchSpec{Image: "image:v1", Executable: "service", Arguments: []string{"serve"}, Environment: []string{"MODE=test"}, WorkingDirectory: "/srv/app",
		Endpoints: []integration.EndpointSpec{{Name: "default", Port: 8080}, {Name: "metrics", Port: 9090}}, Limits: integration.ResourceLimits{MemoryBytes: 1024, MilliCPU: 500, PIDs: 32}}}
	adapter := definitionAdapter{source: definition}
	spec, err := executionSpec(validServerSpec(), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	placement := workload.Placement{Driver: DriverDocker, BindHost: "127.0.0.1", Ports: map[string]int{"default": 8080, "metrics": 9090}}
	launch, err := adapter.Prepare(t.Context(), spec, placement)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(definition.got.Ports, placement.Ports) || launch.Image != "image:v1" || launch.WorkingDirectory != "/srv/app" || len(launch.Endpoints) != 2 || launch.Limits.MilliCPU != 500 || launch.Limits.PIDs != 32 || launch.Limits.MemoryBytes != 1024 {
		t.Fatalf("authoring data lost: %+v %+v", definition.got, launch)
	}
	if !reflect.DeepEqual(launch.Arguments, definition.launch.Arguments) || !reflect.DeepEqual(launch.Environment, definition.launch.Environment) {
		t.Fatal("arguments or environment changed")
	}
	spec.Revision++
	if _, err := adapter.Prepare(t.Context(), spec, placement); err == nil {
		t.Fatal("accepted mismatched execution envelope")
	}
}

func TestWorkloadAdapterRejectsAmbiguousRouteAndCanceledPreparation(t *testing.T) {
	definition := &placementDefinition{launch: LaunchSpec{Endpoints: []integration.EndpointSpec{{Name: "http", Port: 8080}, {Name: "metrics", Port: 9090}}}}
	adapter := definitionAdapter{source: definition}
	spec, err := executionSpec(validServerSpec(), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Prepare(t.Context(), spec, workload.Placement{}); err == nil {
		t.Fatal("multiple endpoints without a route selection accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := adapter.Prepare(ctx, spec, workload.Placement{}); err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
}
