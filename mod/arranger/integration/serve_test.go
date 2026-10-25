package integration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type testExtension struct{}

func (testExtension) Type() string                   { return "example" }
func (testExtension) Supports(name string) bool      { return name == DriverLocal }
func (testExtension) Validate(spec ServerSpec) error { return nil }
func (testExtension) Launch(spec ServerSpec, runtime, host string, port int) (LaunchSpec, error) {
	return LaunchSpec{Executable: "example", Port: port}, nil
}

func TestServeNegotiatesAndReturnsWorkloadResults(t *testing.T) {
	var input bytes.Buffer
	for _, request := range []ProvisionRequest{
		{Version: ProvisionVersion, ID: 1, Method: "describe"},
		{Version: ProvisionVersion, ID: 2, Method: "launch", Spec: &ServerSpec{}, Runtime: DriverLocal, Port: 1234},
		{Version: ProvisionVersion + 1, ID: 3, Method: "describe"},
	} {
		encoded, err := EncodeProvisionRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		input.Write(encoded)
	}
	var output bytes.Buffer
	if err := ServeProvisioner(testExtension{}, &input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var first, second, third ProvisionResponse
	for _, response := range []*ProvisionResponse{&first, &second, &third} {
		if err := decoder.Decode(response); err != nil {
			t.Fatal(err)
		}
	}
	if first.Type != "example" || len(first.Drivers) != 1 || first.Drivers[0] != DriverLocal {
		t.Fatalf("bad handshake: %+v", first)
	}
	if second.Launch == nil || second.Launch.Executable != "example" || second.Launch.Port != 1234 {
		t.Fatalf("bad launch: %+v", second)
	}
	if !strings.Contains(third.Error, "version") {
		t.Fatalf("missing compatibility error: %+v", third)
	}
}
