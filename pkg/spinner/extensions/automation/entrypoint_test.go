package automationextensions

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

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

	launch, err := spinnerLaunch(installed)
	if err != nil {
		t.Fatalf("select Spinner entrypoint: %v", err)
	}
	if launch == nil || launch.Path != filepath.Join(directory, "spinner") || launch.Dir != directory {
		t.Fatalf("unexpected Spinner launch: %#v", launch)
	}
}

func TestSpinnerLaunchKeepsLegacyFirstEntrypointFallback(t *testing.T) {
	launch, err := spinnerLaunch(store.Installed{})
	if err != nil {
		t.Fatalf("select legacy entrypoint: %v", err)
	}
	if launch != nil {
		t.Fatalf("legacy package unexpectedly overrode ext launch: %#v", launch)
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
