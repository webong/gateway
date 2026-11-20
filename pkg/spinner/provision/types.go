package provision

import (
	"github.com/webong/gateway/arranger/integration"
)

// These aliases keep Spinner's provisioning implementation focused on hosting
// while extension authors depend only on the independently versioned Arranger
// module.
type ServerSpec = integration.ServerSpec
type LaunchSpec = integration.LaunchSpec
type Provisioner = integration.Provisioner

// InstanceEndpoint is the versioned Planner wire format, independent of engine structs.
type InstanceEndpoint struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// InstanceReport is Spinner's observed state for one reconciled instance. It
// is sent to Planner and is not part of the extension-authoring interface.
type InstanceReport struct {
	Endpoints []InstanceEndpoint `json:"endpoints,omitempty"`
	NodeID    string             `json:"node_id"`
	Runtime   string             `json:"runtime"`
	PID       int                `json:"pid,omitempty"`
	Host      string             `json:"host,omitempty"`
	Port      int                `json:"port,omitempty"`
	State     string             `json:"state"`
	Revision  int64              `json:"revision"`
	Error     string             `json:"error,omitempty"`
}

// Logger is the host diagnostic surface consumed by Workload.
type Logger interface {
	Debug(string, ...interface{})
	Info(string, ...interface{})
	Warn(string, ...interface{})
	Error(string, ...interface{})
}

const (
	DesiredRunning   = integration.DesiredRunning
	DesiredStopped   = integration.DesiredStopped
	DriverLocal      = integration.DriverLocal
	DriverDocker     = integration.DriverDocker
	DriverKubernetes = integration.DriverKubernetes
)
