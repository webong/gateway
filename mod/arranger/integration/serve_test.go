package integration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webong/gateway/arranger"
)

type testExtension struct{}

func (testExtension) Type() string                            { return "example" }
func (testExtension) Supports(name string) bool               { return name == arranger.DriverLocal }
func (testExtension) Validate(spec arranger.ServerSpec) error { return nil }
func (testExtension) Launch(spec arranger.ServerSpec, runtime, host string, port int) (arranger.LaunchSpec, error) {
	return arranger.LaunchSpec{Executable: "example", Port: port}, nil
}

func TestServeNegotiatesAndReturnsWorkloadResults(t *testing.T) {
	var input bytes.Buffer
	for _, request := range []Request{
		{Version: Version, ID: 1, Method: "describe"},
		{Version: Version, ID: 2, Method: "launch", Spec: &arranger.ServerSpec{}, Runtime: arranger.DriverLocal, Port: 1234},
		{Version: Version + 1, ID: 3, Method: "describe"},
	} {
		encoded, err := Encode(request)
		if err != nil {
			t.Fatal(err)
		}
		input.Write(encoded)
	}
	var output bytes.Buffer
	if err := Serve(testExtension{}, &input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var first, second, third Response
	for _, response := range []*Response{&first, &second, &third} {
		if err := decoder.Decode(response); err != nil {
			t.Fatal(err)
		}
	}
	if first.Type != "example" || len(first.Drivers) != 1 || first.Drivers[0] != arranger.DriverLocal {
		t.Fatalf("bad handshake: %+v", first)
	}
	if second.Launch == nil || second.Launch.Executable != "example" || second.Launch.Port != 1234 {
		t.Fatalf("bad launch: %+v", second)
	}
	if !strings.Contains(third.Error, "version") {
		t.Fatalf("missing compatibility error: %+v", third)
	}
}
