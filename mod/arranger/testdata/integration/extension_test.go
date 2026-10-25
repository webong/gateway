package example

import (
	"testing"

	"github.com/webong/gateway/arranger/integration"
)

func TestIntegrationUsesPublishedArrangerContracts(t *testing.T) {
	spec := integration.ServerSpec{AssignmentID: "a", ID: "s", Type: "example", Hostname: "example.test", Driver: integration.DriverLocal, DesiredState: integration.DesiredRunning, Replicas: 1, Revision: 1}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	extension := Extension{}
	if err := extension.Validate(spec); err != nil {
		t.Fatal(err)
	}
	launch, err := extension.Launch(spec, spec.Driver, "127.0.0.1", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if launch.Executable != "example" || launch.Port != 8080 {
		t.Fatalf("unexpected launch: %+v", launch)
	}
}
