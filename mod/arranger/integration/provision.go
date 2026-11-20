package integration

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	DesiredRunning   = "running"
	DesiredStopped   = "stopped"
	DriverLocal      = "local"
	DriverDocker     = "docker"
	DriverKubernetes = "kubernetes"
)

type ServerSpec struct {
	AssignmentID  string          `json:"assignment_id"`
	TargetID      string          `json:"target_id,omitempty"`
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Name          string          `json:"name"`
	Hostname      string          `json:"hostname"`
	Path          string          `json:"path"`
	Driver        string          `json:"driver"`
	DesiredState  string          `json:"desired_state"`
	Replicas      int             `json:"replicas"`
	Revision      int64           `json:"revision"`
	Configuration json.RawMessage `json:"configuration"`
}

func (s ServerSpec) Validate() error {
	if strings.TrimSpace(s.AssignmentID) == "" {
		return fmt.Errorf("provisioned server assignment ID is required")
	}
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("provisioned server ID is required")
	}
	if strings.TrimSpace(s.Type) == "" {
		return fmt.Errorf("provisioned server %q type is required", s.ID)
	}
	if strings.TrimSpace(s.Hostname) == "" {
		return fmt.Errorf("provisioned server %q hostname is required", s.ID)
	}
	if s.Driver != DriverLocal && s.Driver != DriverDocker && s.Driver != DriverKubernetes {
		return fmt.Errorf("provisioned server %q uses unsupported driver %q", s.ID, s.Driver)
	}
	if s.DesiredState != DesiredRunning && s.DesiredState != DesiredStopped {
		return fmt.Errorf("provisioned server %q has invalid desired state %q", s.ID, s.DesiredState)
	}
	if s.Replicas < 1 {
		return fmt.Errorf("provisioned server %q must request at least one replica", s.ID)
	}
	if s.Revision < 1 {
		return fmt.Errorf("provisioned server %q revision must be positive", s.ID)
	}

	return nil
}

// EndpointSpec declares an application port without coupling authoring to a runtime.
type EndpointSpec struct {
	Name string
	Port int
}

// ResourceLimits are enforced by the selected driver; unsupported limits fail closed.
type ResourceLimits struct {
	MemoryBytes int64
	MilliCPU    int64
	PIDs        int64
}

type LaunchSpec struct {
	Endpoints        []EndpointSpec
	Limits           ResourceLimits
	Image            string
	Executable       string
	Arguments        []string
	Environment      []string
	WorkingDirectory string
	Port             int
	ContainerName    string
}

// Provisioner describes a managed technology independently of the host that runs it.
// Validate checks technology configuration; Launch returns a specification
// without starting processes. The host selects the runtime and executes it.
type Provisioner interface {
	Type() string
	Supports(runtime string) bool
	Validate(spec ServerSpec) error
	Launch(spec ServerSpec, runtime, host string, port int) (LaunchSpec, error)
}

// Placement supplies reusable named ports when a local service is recovered.
// Definitions describe a launch; they never bind listeners or start processes.
type Placement struct {
	Driver   string
	BindHost string
	Ports    map[string]int
}

// PlacementProvisioner is optional. Legacy single-port definitions keep using Launch.
type PlacementProvisioner interface {
	LaunchPlacement(ServerSpec, Placement) (LaunchSpec, error)
}
