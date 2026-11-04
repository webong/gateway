package host

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
)

func TestSpinnerLaunchSelectsNamedEntrypointFromCombinedPackage(t *testing.T) {
	directory := t.TempDir()
	planner := writeEntrypointArtifact(t, directory, "planner")
	spinner := writeEntrypointArtifact(t, directory, "spinner")
	installed := store.Installed{
		Directory: directory,
		Manifest: packagekit.Manifest{
			Artifacts: []packagekit.Artifact{
				{Name: "planner", Path: "planner", SHA256: planner},
				{Name: "spinner", Path: "spinner", SHA256: spinner},
			},
			Entrypoints: []packagekit.Entrypoint{
				{Name: "planner", Artifact: "planner"},
				{Name: "spinner", Artifact: "spinner"},
			},
		},
	}

	launch, err := SpinnerLaunch(installed)
	if err != nil {
		t.Fatalf("select Spinner entrypoint: %v", err)
	}
	if launch == nil || launch.Path != filepath.Join(directory, "spinner") || launch.Dir != directory {
		t.Fatalf("unexpected Spinner launch: %#v", launch)
	}
}

func TestSpinnerLaunchKeepsLegacyFirstEntrypointFallback(t *testing.T) {
	launch, err := SpinnerLaunch(store.Installed{})
	if err != nil {
		t.Fatalf("select legacy entrypoint: %v", err)
	}
	if launch != nil {
		t.Fatalf("legacy package unexpectedly overrode ext launch: %#v", launch)
	}
}

func TestSpinnerLaunchRejectsPackageBuiltForAnotherPlatform(t *testing.T) {
	metadata, err := json.Marshal(map[string]any{
		"platform": map[string]string{"os": "not-" + runtime.GOOS, "arch": runtime.GOARCH},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = SpinnerLaunch(store.Installed{Manifest: packagekit.Manifest{
		Metadata: map[string]json.RawMessage{"gateway.spinner/v1": metadata},
	}})
	if err == nil || !strings.Contains(err.Error(), "does not match host") {
		t.Fatalf("platform mismatch was not rejected clearly: %v", err)
	}
}

// A package whose metadata ext rejects cannot be installed, so the environment
// key must pass ext's manifest validation, not just be readable by Spinner.
func TestSpinnerLaunchInheritsEnvironmentDeclaredByAValidPackage(t *testing.T) {
	t.Setenv("GATEWAY_SHELL_HOOKS_DIR", "/opt/gateway/hooks")
	directory := t.TempDir()
	spinner := writeEntrypointArtifact(t, directory, "spinner")
	environment, err := json.Marshal(map[string]any{
		"inherit": []string{"GATEWAY_SHELL_HOOKS_DIR", "GATEWAY_SHELL_NOT_SET"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest := packagekit.Manifest{
		APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{
			APIVersion: plugin.APIVersion,
			Identity:   plugin.Identity{ID: "webong/shell", Revision: "r1", Version: "1.0.0"},
			Contracts: []plugin.Contract{{
				ContractRef: plugin.ContractRef{Name: "gateway.automation", Version: "v1"},
				Operations:  []plugin.Operation{{Name: "execute", Surface: "shell"}},
			}},
		},
		Artifacts: []packagekit.Artifact{{Name: "spinner", Path: "spinner", SHA256: spinner}},
		Entrypoints: []packagekit.Entrypoint{{
			Name: "spinner", Runtime: "process", Artifact: "spinner", Protocols: []string{plugin.APIVersion},
		}},
		Metadata: map[string]json.RawMessage{spinnerEnvironmentMetadata: environment},
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("ext rejects the Spinner environment metadata key %q: %v", spinnerEnvironmentMetadata, err)
	}

	launch, err := SpinnerLaunch(store.Installed{Directory: directory, Manifest: manifest})
	if err != nil {
		t.Fatalf("select Spinner entrypoint: %v", err)
	}
	if launch == nil || len(launch.Env) != 1 || launch.Env[0] != "GATEWAY_SHELL_HOOKS_DIR=/opt/gateway/hooks" {
		t.Fatalf("package did not inherit exactly its declared, set variables: %#v", launch)
	}
}

func writeEntrypointArtifact(t *testing.T, directory, name string) string {
	t.Helper()
	contents := []byte(name)
	if err := os.WriteFile(filepath.Join(directory, name), contents, 0o700); err != nil {
		t.Fatalf("write %s artifact: %v", name, err)
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}
