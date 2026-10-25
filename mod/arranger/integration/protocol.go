// Package integration defines the versioned stdio protocol for executable
// Gateway integrations. It depends only on the published Arranger module.
package integration

import "github.com/webong/gateway/arranger"

const Version = 1
const MaxMessageBytes = 8 << 20

type Request struct {
	Version int                  `json:"version"`
	ID      uint64               `json:"id"`
	Method  string               `json:"method"`
	Spec    *arranger.ServerSpec `json:"spec,omitempty"`
	Runtime string               `json:"runtime,omitempty"`
	Host    string               `json:"host,omitempty"`
	Port    int                  `json:"port,omitempty"`
}

type Response struct {
	Version int                  `json:"version"`
	ID      uint64               `json:"id"`
	Type    string               `json:"type,omitempty"`
	Drivers []string             `json:"drivers,omitempty"`
	Launch  *arranger.LaunchSpec `json:"launch,omitempty"`
	Error   string               `json:"error,omitempty"`
}
