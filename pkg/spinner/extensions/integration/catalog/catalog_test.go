package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testArtifact(url string, payload []byte) Artifact {
	digest := sha256.Sum256(payload)
	return Artifact{Name: "sample", Version: "v1.2.3", OS: runtime.GOOS, Arch: runtime.GOARCH, URL: url, SHA256: hex.EncodeToString(digest[:])}
}

func writeCatalog(t *testing.T, path string, artifact Artifact) {
	t.Helper()
	data, err := json.Marshal(Catalog{SchemaVersion: SchemaVersion, Artifacts: []Artifact{artifact}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallLocalCatalogAndVerifyReceipt(t *testing.T) {
	root := t.TempDir()
	assetDir := filepath.Join(root, "assets")
	if err := os.Mkdir(assetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(filepath.Join(assetDir, "sample"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(root, "catalog.json")
	writeCatalog(t, reference, testArtifact("assets/sample", payload))
	request := InstallRequest{Catalog: reference, Name: "sample", Version: "v1.2.3", Dir: filepath.Join(root, "installed")}
	path, err := Install(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(request.Dir, "sample", "v1.2.3", "gateway-sample") {
		t.Fatalf("unexpected installed path %q", path)
	}
	receipt, err := VerifyInstalled(path)
	if err != nil || receipt.SHA256 != testArtifact("assets/sample", payload).SHA256 {
		t.Fatalf("installed receipt failed verification: %+v, %v", receipt, err)
	}
	if _, err := Install(context.Background(), request); err == nil {
		t.Fatal("installed version was overwritten")
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyInstalled(path); err == nil {
		t.Fatal("tampered integration passed verification")
	}
}

func TestCreateLocalCatalogForVersionedBundle(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"reverb", "mercure"} {
		if err := os.WriteFile(filepath.Join(root, "gateway-"+name), []byte("#!/bin/sh\nexit 0\n"), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	reference, err := CreateLocal(root, "v2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Read(context.Background(), reference, nil)
	if err != nil || len(manifest.Artifacts) != 2 || manifest.Artifacts[0].Name != "mercure" || manifest.Artifacts[1].Name != "reverb" {
		t.Fatalf("generated catalog was invalid: %+v, %v", manifest, err)
	}
	path, err := Install(context.Background(), InstallRequest{
		Catalog: reference, Name: "reverb", Version: "v2.3.4", Dir: filepath.Join(t.TempDir(), "installed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyInstalled(path); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLocal(root, "v2.3.4"); err == nil {
		t.Fatal("existing catalog was overwritten")
	}
}

func TestCreatePlatformBundleSelectsHostArtifact(t *testing.T) {
	root := t.TempDir()
	otherOS := "linux"
	if runtime.GOOS == otherOS {
		otherOS = "darwin"
	}
	for _, platform := range []struct{ os, arch, payload string }{
		{runtime.GOOS, runtime.GOARCH, "host binary"},
		{otherOS, runtime.GOARCH, "other binary"},
	} {
		dir := filepath.Join(root, platform.os, platform.arch)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "gateway-sample"), []byte(platform.payload), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	reference, err := CreatePlatformBundle(root, "v2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := Read(context.Background(), reference, nil)
	if err != nil || len(manifest.Artifacts) != 2 {
		t.Fatalf("generated multi-platform catalog was invalid: %+v, %v", manifest, err)
	}
	path, err := Install(context.Background(), InstallRequest{
		Catalog: reference, Name: "sample", Version: "v2.3.4", Dir: filepath.Join(t.TempDir(), "installed"),
	})
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "host binary" {
		t.Fatalf("installer selected wrong platform: %q, %v", contents, err)
	}
	if _, err := VerifyInstalled(path); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePlatformBundle(root, "v2.3.4"); err == nil {
		t.Fatal("existing catalog was overwritten")
	}
}

func TestCreatePlatformBundleRejectsEmptyAndSymlinkedArtifacts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "linux", "amd64")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := CreatePlatformBundle(root, "v1.0.0"); err == nil {
		t.Fatal("empty platform directory was accepted")
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "external"), filepath.Join(dir, "gateway-sample")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := CreatePlatformBundle(root, "v1.0.0"); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlinked platform artifact was accepted: %v", err)
	}
}

func TestInstallRejectsWrongDigestWithoutExposingVersion(t *testing.T) {
	root := t.TempDir()
	payload := []byte("one")
	if err := os.WriteFile(filepath.Join(root, "sample"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := testArtifact("sample", []byte("different"))
	reference := filepath.Join(root, "catalog.json")
	writeCatalog(t, reference, artifact)
	dir := filepath.Join(root, "installed")
	if _, err := Install(context.Background(), InstallRequest{Catalog: reference, Name: "sample", Version: "v1.2.3", Dir: dir}); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("wrong digest was accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sample", "v1.2.3")); !os.IsNotExist(err) {
		t.Fatalf("failed download exposed an installation directory: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "sample"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed download left staging files: %v, %v", entries, err)
	}
}

func TestInstallRejectsSymlinkedIntegrationDirectory(t *testing.T) {
	root := t.TempDir()
	payload := []byte("binary")
	if err := os.WriteFile(filepath.Join(root, "sample"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(root, "catalog.json")
	writeCatalog(t, reference, testArtifact("sample", payload))
	dir := filepath.Join(root, "installed")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "sample")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Install(context.Background(), InstallRequest{Catalog: reference, Name: "sample", Version: "v1.2.3", Dir: dir}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked installation directory was accepted: %v", err)
	}
}

func TestHTTPSCatalogAndDownloadRejectsDowngrade(t *testing.T) {
	payload := []byte("#!/bin/sh\nexit 0\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/catalog.json":
			_ = json.NewEncoder(writer).Encode(Catalog{SchemaVersion: SchemaVersion, Artifacts: []Artifact{testArtifact("artifact", payload)}})
		case "/artifact":
			_, _ = writer.Write(payload)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	path, err := Install(context.Background(), InstallRequest{
		Catalog: server.URL + "/catalog.json", Name: "sample", Version: "v1.2.3",
		Dir: filepath.Join(t.TempDir(), "installed"), Client: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyInstalled(path); err != nil {
		t.Fatal(err)
	}
	// Exercise the redirect with a second catalog, without changing the
	// successfully installed version above.
	redirectCatalog := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/catalog.json" {
			_ = json.NewEncoder(writer).Encode(Catalog{SchemaVersion: SchemaVersion, Artifacts: []Artifact{testArtifact("redirect", payload)}})
			return
		}
		http.Redirect(writer, request, "http://127.0.0.1:9/not-https", http.StatusFound)
	}))
	defer redirectCatalog.Close()
	_, err = Install(context.Background(), InstallRequest{
		Catalog: redirectCatalog.URL + "/catalog.json", Name: "sample", Version: "v1.2.3",
		Dir: filepath.Join(t.TempDir(), "installed"), Client: redirectCatalog.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "redirected away from HTTPS") {
		t.Fatalf("HTTPS downgrade was accepted: %v", err)
	}
}

func TestCatalogRejectsOversizeDuplicatesAndPathTraversal(t *testing.T) {
	root := t.TempDir()
	artifact := testArtifact("../escape", []byte("binary"))
	reference := filepath.Join(root, "catalog.json")
	writeCatalog(t, reference, artifact)
	if _, err := Install(context.Background(), InstallRequest{Catalog: reference, Name: "sample", Version: "v1.2.3", Dir: filepath.Join(root, "installed")}); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("local path traversal was accepted: %v", err)
	}
	data, _ := json.Marshal(Catalog{SchemaVersion: SchemaVersion, Artifacts: []Artifact{artifact, artifact}})
	if err := os.WriteFile(reference, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), reference, nil); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate artifact was accepted: %v", err)
	}
	oversize := append([]byte(`{"schema_version":1,"artifacts":[]}`), []byte(strings.Repeat(" ", int(maxCatalogBytes)))...)
	if err := os.WriteFile(reference, oversize, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), reference, nil); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversize catalog was accepted: %v", err)
	}
}

func TestSignedCatalogRequiresPinnedKeyAndExactBytes(t *testing.T) {
	root := t.TempDir()
	privatePath := filepath.Join(root, "publisher.key")
	publicPath := filepath.Join(root, "publisher.pub")
	if err := GenerateSigningKeys(privatePath, publicPath); err != nil {
		t.Fatal(err)
	}
	privateInfo, err := os.Stat(privatePath)
	if err != nil || privateInfo.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private key permissions are too broad: %v, %v", privateInfo, err)
	}
	public, err := LoadPublicKey(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("signed binary")
	if err := os.WriteFile(filepath.Join(root, "gateway-sample"), payload, 0o750); err != nil {
		t.Fatal(err)
	}
	reference, err := CreateLocal(root, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SignCatalog(reference, privatePath); err != nil {
		t.Fatal(err)
	}
	if _, err := SignCatalog(reference, privatePath); err == nil {
		t.Fatal("existing signature was overwritten")
	}
	manifest, err := ReadSigned(context.Background(), reference, public, nil)
	if err != nil || len(manifest.Artifacts) != 1 {
		t.Fatalf("signed catalog verification failed: %+v, %v", manifest, err)
	}
	path, err := Install(context.Background(), InstallRequest{
		Catalog: reference, Name: "sample", Version: "v1.2.3", Dir: filepath.Join(root, "installed"), PublicKey: public,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyInstalled(path); err != nil {
		t.Fatal(err)
	}
	otherPrivate := filepath.Join(root, "other.key")
	otherPublic := filepath.Join(root, "other.pub")
	if err := GenerateSigningKeys(otherPrivate, otherPublic); err != nil {
		t.Fatal(err)
	}
	wrongKey, err := LoadPublicKey(otherPublic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSigned(context.Background(), reference, wrongKey, nil); err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("wrong publisher key was accepted: %v", err)
	}
	contents, err := os.ReadFile(reference)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reference, append(contents, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSigned(context.Background(), reference, public, nil); err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Fatalf("modified catalog was accepted: %v", err)
	}
}

func TestReadSignedHTTPSCatalog(t *testing.T) {
	root := t.TempDir()
	privatePath := filepath.Join(root, "publisher.key")
	publicPath := filepath.Join(root, "publisher.pub")
	if err := GenerateSigningKeys(privatePath, publicPath); err != nil {
		t.Fatal(err)
	}
	reference := filepath.Join(root, "catalog.json")
	writeCatalog(t, reference, testArtifact("gateway-sample", []byte("binary")))
	if _, err := SignCatalog(reference, privatePath); err != nil {
		t.Fatal(err)
	}
	public, err := LoadPublicKey(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("token") != "test" {
			http.Error(writer, "missing token", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/catalog.json":
			http.ServeFile(writer, request, reference)
		case "/catalog.json.sig":
			http.ServeFile(writer, request, reference+".sig")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	manifest, err := ReadSigned(context.Background(), server.URL+"/catalog.json?token=test", public, server.Client())
	if err != nil || len(manifest.Artifacts) != 1 {
		t.Fatalf("signed HTTPS catalog failed: %+v, %v", manifest, err)
	}
}
