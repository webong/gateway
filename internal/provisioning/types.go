package provisioning

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

type LaunchSpec struct {
	Image            string
	Executable       string
	Arguments        []string
	Environment      []string
	WorkingDirectory string
	Port             int
	ContainerName    string
}

type Workload interface {
	Type() string
	Supports(string) bool
	Validate(ServerSpec) error
	Launch(ServerSpec, string, string, int) (LaunchSpec, error)
}

type InstanceReport struct {
	NodeID   string `json:"node_id"`
	Runtime  string `json:"runtime"`
	PID      int    `json:"pid,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	State    string `json:"state"`
	Revision int64  `json:"revision"`
	Error    string `json:"error,omitempty"`
}
