package workload

import (
	"fmt"

	"github.com/webong/gateway/driver"
)

type Workload struct{}

func (Workload) Type() string                 { return "example" }
func (Workload) Supports(runtime string) bool { return runtime == driver.DriverLocal }

func (w Workload) Validate(spec driver.ServerSpec) error {
	if spec.Type != w.Type() || !w.Supports(spec.Driver) {
		return fmt.Errorf("unsupported workload or runtime")
	}
	return nil
}

func (Workload) Launch(_ driver.ServerSpec, runtime, host string, port int) (driver.LaunchSpec, error) {
	if runtime != driver.DriverLocal {
		return driver.LaunchSpec{}, fmt.Errorf("unsupported runtime %q", runtime)
	}
	return driver.LaunchSpec{Executable: "example", Arguments: []string{"--host", host}, Port: port}, nil
}

var _ driver.Workload = Workload{}
