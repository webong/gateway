package integration

import (
	"testing"

	arrangerintegration "github.com/webong/gateway/arranger/integration"
)

func TestProvisionerUsesPublishedArrangerContracts(t *testing.T) {
	spec := arrangerintegration.ServerSpec{AssignmentID: "a", ID: "s", Type: "example", Hostname: "example.test", Driver: arrangerintegration.DriverLocal, DesiredState: arrangerintegration.DesiredRunning, Replicas: 1, Revision: 1}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	provisioner := Provisioner{}
	if err := provisioner.Validate(spec); err != nil {
		t.Fatal(err)
	}
	launch, err := provisioner.Launch(spec, spec.Driver, "127.0.0.1", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if launch.Executable != "example" || launch.Port != 8080 {
		t.Fatalf("unexpected launch: %+v", launch)
	}
}
