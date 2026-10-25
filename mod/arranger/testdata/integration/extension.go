package example

import (
	"fmt"

	"github.com/webong/gateway/arranger/integration"
)

type Extension struct{}

func (Extension) Type() string                 { return "example" }
func (Extension) Supports(runtime string) bool { return runtime == integration.DriverLocal }

func (e Extension) Validate(spec integration.ServerSpec) error {
	if spec.Type != e.Type() || !e.Supports(spec.Driver) {
		return fmt.Errorf("unsupported workload or runtime")
	}
	return nil
}

func (Extension) Launch(_ integration.ServerSpec, runtime, host string, port int) (integration.LaunchSpec, error) {
	if runtime != integration.DriverLocal {
		return integration.LaunchSpec{}, fmt.Errorf("unsupported runtime %q", runtime)
	}
	return integration.LaunchSpec{Executable: "example", Arguments: []string{"--host", host}, Port: port}, nil
}

var _ integration.Provisioner = Extension{}
