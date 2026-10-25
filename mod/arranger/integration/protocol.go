// Package integration defines the shared Integration capability: managed
// service provisioning, traffic planning and delivery, installable Spinner
// runtimes, and runtime-neutral HTTP composition.
package integration

const ProvisionVersion = 1
const ProvisionMaxMessageBytes = 8 << 20

type ProvisionRequest struct {
	Version int         `json:"version"`
	ID      uint64      `json:"id"`
	Method  string      `json:"method"`
	Spec    *ServerSpec `json:"spec,omitempty"`
	Runtime string      `json:"runtime,omitempty"`
	Host    string      `json:"host,omitempty"`
	Port    int         `json:"port,omitempty"`
}

type ProvisionResponse struct {
	Version int         `json:"version"`
	ID      uint64      `json:"id"`
	Type    string      `json:"type,omitempty"`
	Drivers []string    `json:"drivers,omitempty"`
	Launch  *LaunchSpec `json:"launch,omitempty"`
	Error   string      `json:"error,omitempty"`
}
