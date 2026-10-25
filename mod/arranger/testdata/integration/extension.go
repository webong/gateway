package example

import (
	"fmt"

	"github.com/webong/gateway/arranger"
)

type Extension struct{}

func (Extension) Type() string                 { return "example" }
func (Extension) Supports(runtime string) bool { return runtime == arranger.DriverLocal }

func (e Extension) Validate(spec arranger.ServerSpec) error {
	if spec.Type != e.Type() || !e.Supports(spec.Driver) {
		return fmt.Errorf("unsupported workload or runtime")
	}
	return nil
}

func (Extension) Launch(_ arranger.ServerSpec, runtime, host string, port int) (arranger.LaunchSpec, error) {
	if runtime != arranger.DriverLocal {
		return arranger.LaunchSpec{}, fmt.Errorf("unsupported runtime %q", runtime)
	}
	return arranger.LaunchSpec{Executable: "example", Arguments: []string{"--host", host}, Port: port}, nil
}

var _ arranger.Extension = Extension{}
