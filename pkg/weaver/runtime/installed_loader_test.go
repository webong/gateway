package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
	arranger "github.com/webong/gateway/arranger/integration"
)

func TestRuntimeStartProbesPrivateLoaderAfterRealStoreInstallation(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The shell builtin creates evidence of execution, then deliberately fails the
	// PHP feature probe. No secrets, workers or private HTTP listener are needed.
	body := []byte("#!/bin/sh\n: > \"${0%/*}/probe-ran\"\nexit 1\n")
	if err := os.WriteFile(filepath.Join(source, "runtime/loader"), body, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	manifest := packagekit.Manifest{APIVersion: packagekit.Version,
		Descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/weaver-loader", Revision: "test"},
			Contracts: []plugin.Contract{{ContractRef: plugin.ContractRef{Name: "gateway.runtime", Version: "v1"}, Operations: []plugin.Operation{{Name: "start"}}}}},
		Artifacts: []packagekit.Artifact{{Name: "loader", Path: "runtime/loader", SHA256: hex.EncodeToString(sum[:])}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, store.ManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	packages, err := store.New(filepath.Join(t.TempDir(), "packages"), store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := packages.Install(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(installed.Directory, "runtime/loader"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("test must exercise real non-entrypoint store permissions: %v", info.Mode())
	}
	service := New(installed.Directory)
	result, err := service.Start(context.Background(), arranger.RuntimeStartRequest{Configuration: json.RawMessage(`{"schema_version":1,"database_ref":"database","authentication_ref":"auth"}`)}, nil)
	if err == nil || len(result.PrivateHTTP) != 0 {
		t.Fatal("deliberately failed PHP probe advertised readiness")
	}
	if _, err := os.Stat(filepath.Join(installed.Directory, "runtime/probe-ran")); err != nil {
		t.Fatalf("verified installed loader could not execute: %v", err)
	}
}
