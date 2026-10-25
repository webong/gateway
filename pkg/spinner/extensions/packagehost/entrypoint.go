// Package packagehost contains package lifecycle helpers shared by Spinner
// capability hosts. It knows ext.package/v1, never product extensions.
package packagehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"

	pluginprocess "github.com/webong/ext/pkg/plugin-go/contract/host/process"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
)

// SpinnerLaunch selects the named Spinner entrypoint from a verified package.
// A nil command preserves ext's first-entrypoint behavior for legacy packages.
func SpinnerLaunch(inst store.Installed) (*pluginprocess.Command, error) {
	if raw, declared := inst.Manifest.Metadata["gateway.spinner/v1"]; declared {
		var metadata struct {
			Platform struct {
				OS   string `json:"os"`
				Arch string `json:"arch"`
			} `json:"platform"`
		}
		if err := json.Unmarshal(raw, &metadata); err != nil || metadata.Platform.OS == "" || metadata.Platform.Arch == "" {
			return nil, errors.New("package declares invalid Spinner platform metadata")
		}
		if metadata.Platform.OS != runtime.GOOS || metadata.Platform.Arch != runtime.GOARCH {
			return nil, fmt.Errorf("package Spinner target %s/%s does not match host %s/%s", metadata.Platform.OS, metadata.Platform.Arch, runtime.GOOS, runtime.GOARCH)
		}
	}
	hasNamedEntrypoint := false
	for _, entrypoint := range inst.Manifest.Entrypoints {
		hasNamedEntrypoint = hasNamedEntrypoint || entrypoint.Name != ""
		if entrypoint.Name != "spinner" {
			continue
		}
		path, err := inst.ArtifactPath(entrypoint.Artifact)
		if err != nil {
			return nil, err
		}
		return &pluginprocess.Command{Path: path, Dir: inst.Directory}, nil
	}
	if hasNamedEntrypoint {
		return nil, errors.New("package does not declare a spinner entrypoint")
	}
	return nil, nil
}
