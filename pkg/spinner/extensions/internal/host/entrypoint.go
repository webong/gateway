// Package host contains package lifecycle helpers shared by Spinner extension
// hosts. It knows ext.package/v1, never product extensions.
package host

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

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
	environment, err := spinnerEnvironment(inst)
	if err != nil {
		return nil, err
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
		return &pluginprocess.Command{Path: path, Dir: inst.Directory, Env: environment}, nil
	}
	if hasNamedEntrypoint {
		return nil, errors.New("package does not declare a spinner entrypoint")
	}
	return nil, nil
}

func spinnerEnvironment(inst store.Installed) ([]string, error) {
	raw, ok := inst.Manifest.Metadata["gateway.spinner/environment/v1"]
	if !ok {
		return nil, nil
	}
	var metadata struct {
		Inherit []string `json:"inherit"`
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, errors.New("package declares invalid Spinner environment metadata")
	}
	seen := make(map[string]struct{}, len(metadata.Inherit))
	values := make([]string, 0, len(metadata.Inherit))
	for _, key := range metadata.Inherit {
		if !validSpinnerEnvironmentName(key) {
			return nil, errors.New("package declares an invalid Spinner environment name")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, errors.New("package declares a duplicate Spinner environment name")
		}
		seen[key] = struct{}{}
		if value, exists := os.LookupEnv(key); exists {
			values = append(values, key+"="+value)
		}
	}
	return values, nil
}

func validSpinnerEnvironmentName(key string) bool {
	if len(key) < len("GATEWAY_A") || len(key) > 128 || !strings.HasPrefix(key, "GATEWAY_") {
		return false
	}
	for _, char := range key {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return !strings.Contains(key, "TOKEN") && !strings.Contains(key, "SECRET") &&
		!strings.Contains(key, "PASSWORD") && !strings.Contains(key, "PRIVATE") &&
		!strings.Contains(key, "CREDENTIAL") && !strings.Contains(key, "KEY")
}
