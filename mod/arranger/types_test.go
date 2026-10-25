package arranger_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/webong/gateway/arranger"
)

func TestPlannerAssignmentRemainsCompatible(t *testing.T) {
	const assignment = `{"assignment_id":"assignment-1","id":"server-1","type":"custom","name":"Example","hostname":"example.test","path":"/events","driver":"docker","desired_state":"running","replicas":2,"revision":3,"configuration":{"port":8080}}`
	var spec arranger.ServerSpec
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
	for _, runtime := range []string{arranger.DriverLocal, arranger.DriverDocker, arranger.DriverKubernetes} {
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
	valid := arranger.ServerSpec{AssignmentID: "assignment-1", ID: "server-1", Type: "custom", Hostname: "example.test", Driver: arranger.DriverLocal, DesiredState: arranger.DesiredStopped, Replicas: 1, Revision: 1}
	for name, change := range map[string]func(*arranger.ServerSpec){
		"assignment": func(s *arranger.ServerSpec) { s.AssignmentID = "" },
		"server":     func(s *arranger.ServerSpec) { s.ID = "" },
		"revision":   func(s *arranger.ServerSpec) { s.Revision = 0 },
		"replicas":   func(s *arranger.ServerSpec) { s.Replicas = 0 },
		"state":      func(s *arranger.ServerSpec) { s.DesiredState = "unknown" },
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
