package provision

import (
	"context"

	"github.com/webong/gateway/arranger/integration"
)

// These aliases keep Spinner's provisioning implementation focused on hosting
// while extension authors depend only on the independently versioned Arranger
// module.
type ServerSpec = integration.ServerSpec
type LaunchSpec = integration.LaunchSpec
type Provisioner = integration.Provisioner

// InstanceReport is Spinner's observed state for one reconciled instance. It
// is sent to Planner and is not part of the extension-authoring interface.
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

// Driver is Spinner's internal adapter for one execution environment. It is
// intentionally not part of Arranger's extension-authoring interface.
type Driver interface {
	Start(context.Context, ServerSpec, string, Provisioner) (Process, error)
}

// Process is a running extension-owned workload managed by Spinner.
type Process interface {
	PID() int
	Host() string
	Port() int
	Done() <-chan struct{}
	Err() error
	Stop(context.Context) error
}

const (
	DesiredRunning   = integration.DesiredRunning
	DesiredStopped   = integration.DesiredStopped
	DriverLocal      = integration.DriverLocal
	DriverDocker     = integration.DriverDocker
	DriverKubernetes = integration.DriverKubernetes
)
