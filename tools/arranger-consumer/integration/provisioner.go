package integration

import (
	"fmt"

	arrangerintegration "github.com/webong/gateway/arranger/integration"
)

type Provisioner struct{}

func (Provisioner) Type() string                 { return "example" }
func (Provisioner) Supports(runtime string) bool { return runtime == arrangerintegration.DriverLocal }

func (p Provisioner) Validate(spec arrangerintegration.ServerSpec) error {
	if spec.Type != p.Type() || !p.Supports(spec.Driver) {
		return fmt.Errorf("unsupported workload or runtime")
	}

	return nil
}

func (Provisioner) Launch(_ arrangerintegration.ServerSpec, runtime, host string, port int) (arrangerintegration.LaunchSpec, error) {
	if runtime != arrangerintegration.DriverLocal {
		return arrangerintegration.LaunchSpec{}, fmt.Errorf("unsupported runtime %q", runtime)
	}

	return arrangerintegration.LaunchSpec{Executable: "example", Arguments: []string{"--host", host}, Port: port}, nil
}

var _ arrangerintegration.Provisioner = Provisioner{}
