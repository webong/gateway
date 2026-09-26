package workload

import (
	"testing"

	"github.com/webong/gateway/driver"
)

func TestExtensionUsesPublishedDriverContracts(t *testing.T) {
	spec := driver.ServerSpec{AssignmentID: "a", ID: "s", Type: "example", Hostname: "example.test", Driver: driver.DriverLocal, DesiredState: driver.DesiredRunning, Replicas: 1, Revision: 1}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	w := Workload{}
	if err := w.Validate(spec); err != nil {
		t.Fatal(err)
	}
	launch, err := w.Launch(spec, spec.Driver, "127.0.0.1", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if launch.Executable != "example" || launch.Port != 8080 {
		t.Fatalf("unexpected launch: %+v", launch)
	}
}
