package workloadplugin

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webong/gateway/driver"
)

type testWorkload struct{}

func (testWorkload) Type() string                          { return "example" }
func (testWorkload) Supports(name string) bool             { return name == driver.DriverLocal }
func (testWorkload) Validate(spec driver.ServerSpec) error { return nil }
func (testWorkload) Launch(spec driver.ServerSpec, runtime, host string, port int) (driver.LaunchSpec, error) {
	return driver.LaunchSpec{Executable: "example", Port: port}, nil
}

func TestServeNegotiatesAndReturnsWorkloadResults(t *testing.T) {
	var input bytes.Buffer
	for _, request := range []Request{
		{Version: Version, ID: 1, Method: "describe"},
		{Version: Version, ID: 2, Method: "launch", Spec: &driver.ServerSpec{}, Runtime: driver.DriverLocal, Port: 1234},
		{Version: Version + 1, ID: 3, Method: "describe"},
	} {
		encoded, err := Encode(request)
		if err != nil {
			t.Fatal(err)
		}
		input.Write(encoded)
	}
	var output bytes.Buffer
	if err := Serve(testWorkload{}, &input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var first, second, third Response
	for _, response := range []*Response{&first, &second, &third} {
		if err := decoder.Decode(response); err != nil {
			t.Fatal(err)
		}
	}
	if first.Type != "example" || len(first.Drivers) != 1 || first.Drivers[0] != driver.DriverLocal {
		t.Fatalf("bad handshake: %+v", first)
	}
	if second.Launch == nil || second.Launch.Executable != "example" || second.Launch.Port != 1234 {
		t.Fatalf("bad launch: %+v", second)
	}
	if !strings.Contains(third.Error, "version") {
		t.Fatalf("missing compatibility error: %+v", third)
	}
}
