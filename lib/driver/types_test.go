package driver_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/webong/gateway/driver"
)

func TestPlannerAssignmentRemainsCompatible(t *testing.T) {
	const assignment = `{"assignment_id":"assignment-1","id":"server-1","type":"custom","name":"Example","hostname":"example.test","path":"/events","driver":"docker","desired_state":"running","replicas":2,"revision":3,"configuration":{"port":8080}}`
	var spec driver.ServerSpec
	if err := json.Unmarshal([]byte(assignment), &spec); err != nil {
		t.Fatal(err)
	}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	if err := json.Unmarshal([]byte(assignment), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("assignment changed on round trip: %s", encoded)
	}
	for _, runtime := range []string{driver.DriverLocal, driver.DriverDocker, driver.DriverKubernetes} {
		spec.Driver = runtime
		if err := spec.Validate(); err != nil {
			t.Fatalf("%s: %v", runtime, err)
		}
	}
	spec.Driver = "unknown"
	if err := spec.Validate(); err == nil {
		t.Fatal("unsupported runtime accepted")
	}
}

func TestAssignmentRequiresIdentityAndRevision(t *testing.T) {
	valid := driver.ServerSpec{AssignmentID: "assignment-1", ID: "server-1", Type: "custom", Hostname: "example.test", Driver: driver.DriverLocal, DesiredState: driver.DesiredStopped, Replicas: 1, Revision: 1}
	for name, change := range map[string]func(*driver.ServerSpec){
		"assignment": func(s *driver.ServerSpec) { s.AssignmentID = "" },
		"server":     func(s *driver.ServerSpec) { s.ID = "" },
		"revision":   func(s *driver.ServerSpec) { s.Revision = 0 },
		"replicas":   func(s *driver.ServerSpec) { s.Replicas = 0 },
		"state":      func(s *driver.ServerSpec) { s.DesiredState = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			spec := valid
			change(&spec)
			if err := spec.Validate(); err == nil {
				t.Fatal("invalid assignment accepted")
			}
		})
	}
}
