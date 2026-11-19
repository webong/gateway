package extensions

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webong/ext/pkg/ext-go/engine/host/store"
)

func TestArchiveStoreAdoptsExistingInstallAndCachesAcrossDesiredRevisions(t *testing.T) {
	archive := hookScriptTestArchive(t)
	root := t.TempDir()
	packages, err := store.New(root, extensionStoreLimits())
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "legacy.tar.gz")
	if err := os.WriteFile(archivePath, archive, 0600); err != nil {
		t.Fatal(err)
	}
	source, err := store.ExtractArchive(archivePath, digestBytes(archive), filepath.Join(t.TempDir(), "source"), extensionStoreLimits())
	if err != nil {
		t.Fatal(err)
	}
	// Existing Gateway directory installs have no Ext archive receipt.
	if _, err := packages.Install(source); err != nil {
		t.Fatal(err)
	}
	downloads := 0
	client := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		downloads++
		deadline, ok := request.Context().Deadline()
		if !ok || time.Until(deadline) > 60*time.Second || time.Until(deadline) < 59*time.Second {
			t.Error("injected download client did not receive Gateway's transfer deadline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive)), Request: request}, nil
	})
	reconciler := &ArtifactReconciler{artifacts: client}
	desired := DesiredExtension{Extension: "webong/hookscript", Version: "1.0.0", Revision: 1,
		Artifact: DesiredArtifact{URL: "https://artifacts.example/package.tar.gz", SHA256: digestBytes(archive)}}
	installed, err := reconciler.materialize(t.Context(), packages, desired)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := packages.Receipt(desired.Extension)
	if err != nil || receipt.SHA256 != desired.Artifact.SHA256 || receipt.Revision != "test-r1" {
		t.Fatalf("Ext did not record the verified archive: %+v %v", receipt, err)
	}
	packages, err = store.New(root, extensionStoreLimits())
	if err != nil {
		t.Fatal(err)
	}
	desired.Revision++
	if _, err := reconciler.materialize(t.Context(), packages, desired); err != nil {
		t.Fatal(err)
	}
	if downloads != 1 {
		t.Fatalf("cached package downloaded %d times", downloads)
	}
	for _, mismatch := range []DesiredExtension{
		func() DesiredExtension { d := desired; d.Version = "2.0.0"; return d }(),
		func() DesiredExtension { d := desired; d.Digest = strings.Repeat("0", 64); return d }(),
	} {
		if _, err := reconciler.materialize(t.Context(), packages, mismatch); err == nil {
			t.Fatal("cached archive bypassed Gateway's desired version/digest check")
		}
	}
	if err := os.WriteFile(filepath.Join(installed.Directory, "bin", "spinner"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.materialize(t.Context(), packages, desired); err == nil {
		t.Fatal("damaged cached installation was repaired or accepted")
	}
	if downloads != 1 {
		t.Fatal("damaged installation triggered an automatic repair download")
	}
}

func TestArchiveStoreKeepsGatewayExpandedSizeLimit(t *testing.T) {
	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	tarred := tar.NewWriter(compressed)
	// Reject the declared expansion before reading or allocating its contents.
	if err := tarred.WriteHeader(&tar.Header{Name: "oversized", Size: (512 << 20) + 1, Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	_ = tarred.Close() // Deliberately truncated after the oversized header.
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	packages, err := store.New(t.TempDir(), extensionStoreLimits())
	if err != nil {
		t.Fatal(err)
	}
	reconciler := &ArtifactReconciler{artifacts: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive.Bytes())), Request: request}, nil
	})}
	_, err = reconciler.materialize(t.Context(), packages, DesiredExtension{
		Extension: "webong/hookscript", Version: "1.0.0", Revision: 1,
		Artifact: DesiredArtifact{URL: "https://artifacts.example/oversized.tar.gz", SHA256: digestBytes(archive.Bytes())},
	})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("Gateway's expanded size limit was lost: %v", err)
	}
	if _, err := packages.Load("webong/hookscript"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("oversized artifact installed: %v", err)
	}
}
