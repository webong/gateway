package automationextensions

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
)

const (
	maxExtensionArchiveBytes = 512 << 20
	maxExtensionArchiveFiles = 250000
)

type DesiredArtifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type DesiredExtension struct {
	Extension string          `json:"extension"`
	Version   string          `json:"version"`
	Digest    string          `json:"digest"`
	Artifact  DesiredArtifact `json:"artifact"`
	Revision  int             `json:"revision"`
}

// ArtifactReconciler independently materializes Spinner's entrypoint from
// Planner-owned extension desired state. It never receives Planner PHP source.
type ArtifactReconciler struct {
	plannerURL string
	token      string
	storeDir   string
	planner    interface {
		Do(*http.Request) (*http.Response, error)
	}
	artifacts interface {
		Do(*http.Request) (*http.Response, error)
	}
	plugins *Registry

	mu     sync.Mutex
	loaded map[string]string
}

func NewArtifactReconciler(
	plannerURL, token, storeDir string,
	plannerClient, artifactClient interface {
		Do(*http.Request) (*http.Response, error)
	},
	plugins *Registry,
) (*ArtifactReconciler, error) {
	base, err := url.Parse(strings.TrimSpace(plannerURL))
	if err != nil || base.Scheme == "" || base.Host == "" || strings.TrimSpace(token) == "" || strings.TrimSpace(storeDir) == "" {
		return nil, errors.New("Planner URL, internal token, and extension store are required")
	}
	if plannerClient == nil {
		plannerClient = http.DefaultClient
	}
	if artifactClient == nil {
		artifactClient = http.DefaultClient
	}
	if plugins == nil {
		plugins = registry
	}
	return &ArtifactReconciler{
		plannerURL: strings.TrimRight(base.String(), "/"), token: token, storeDir: storeDir,
		planner: plannerClient, artifacts: artifactClient, plugins: plugins, loaded: map[string]string{},
	}, nil
}

func (r *ArtifactReconciler) Reconcile(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	desired, err := r.desired(ctx)
	if err != nil {
		r.closeExternalExtensions()
		return err
	}
	packages, err := store.New(r.storeDir, store.Limits{})
	if err != nil {
		return fmt.Errorf("open extension store: %w", err)
	}
	desiredIDs := make(map[string]bool, len(desired))
	for _, extension := range desired {
		desiredIDs[extension.Extension] = true
		installed, err := r.materialize(ctx, packages, extension)
		if err != nil {
			r.plugins.UnregisterExtension(extension.Extension)
			delete(r.loaded, extension.Extension)
			return fmt.Errorf("reconcile extension %s: %w", extension.Extension, err)
		}
		loadedRevision := fmt.Sprintf("%d:%s:%s", extension.Revision, extension.Artifact.SHA256, installed.Manifest.Descriptor.Identity.Revision)
		if r.loaded[extension.Extension] == loadedRevision {
			continue
		}
		if err := loadExternalInstalled(ctx, r.plugins, installed); err != nil {
			r.plugins.UnregisterExtension(extension.Extension)
			delete(r.loaded, extension.Extension)
			return fmt.Errorf("launch Spinner entrypoint: %w", err)
		}
		r.loaded[extension.Extension] = loadedRevision
	}
	for _, extension := range r.plugins.ExternalExtensions() {
		if desiredIDs[extension] {
			continue
		}
		r.plugins.UnregisterExtension(extension)
		delete(r.loaded, extension)
	}
	return nil
}

func (r *ArtifactReconciler) closeExternalExtensions() {
	for _, extension := range r.plugins.ExternalExtensions() {
		r.plugins.UnregisterExtension(extension)
		delete(r.loaded, extension)
	}
}

func (r *ArtifactReconciler) desired(ctx context.Context) ([]DesiredExtension, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.plannerURL+"/_internal/gateway/extensions", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Gateway-Internal", r.token)
	response, err := r.planner.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query extension desired state: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query extension desired state: Planner returned %s", response.Status)
	}
	var document struct {
		Data []DesiredExtension `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode extension desired state: %w", err)
	}
	seen := map[string]bool{}
	for _, extension := range document.Data {
		if seen[extension.Extension] || !validDesiredExtension(extension) {
			return nil, errors.New("Planner returned invalid extension desired state")
		}
		seen[extension.Extension] = true
	}
	return document.Data, nil
}

func validDesiredExtension(extension DesiredExtension) bool {
	artifact, err := url.Parse(extension.Artifact.URL)
	return err == nil && extension.Extension != "" && extension.Version != "" && extension.Revision > 0 &&
		artifact.Scheme == "https" && artifact.Host != "" && artifact.User == nil && artifact.Fragment == "" &&
		validSHA256(extension.Artifact.SHA256) &&
		(extension.Digest == "" || validSHA256(extension.Digest))
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

type artifactReceipt struct {
	Extension       string `json:"extension"`
	Version         string `json:"version"`
	Revision        int    `json:"revision"`
	ArtifactSHA256  string `json:"artifact_sha256"`
	PackageRevision string `json:"package_revision"`
}

func (r *ArtifactReconciler) materialize(ctx context.Context, packages *store.Store, desired DesiredExtension) (store.Installed, error) {
	if installed, err := packages.Load(desired.Extension); err == nil {
		receipt, receiptErr := r.readReceipt(desired.Extension)
		if receiptErr == nil && receipt.Extension == desired.Extension && receipt.Version == desired.Version &&
			receipt.Revision == desired.Revision && receipt.ArtifactSHA256 == desired.Artifact.SHA256 &&
			receipt.PackageRevision == installed.Manifest.Descriptor.Identity.Revision {
			if err := r.verifyInstalled(installed, desired); err != nil {
				return store.Installed{}, err
			}
			return installed, nil
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.Installed{}, err
	}

	source, cleanup, err := r.downloadAndExtract(ctx, desired.Artifact)
	if err != nil {
		return store.Installed{}, err
	}
	defer cleanup()

	installed, loadErr := packages.Load(desired.Extension)
	if errors.Is(loadErr, store.ErrNotFound) {
		installed, err = packages.Install(source)
	} else if loadErr != nil {
		return store.Installed{}, loadErr
	} else {
		installed, err = packages.Upgrade(source)
	}
	if err != nil {
		return store.Installed{}, err
	}
	if err := r.verifyInstalled(installed, desired); err != nil {
		return store.Installed{}, err
	}
	if err := r.writeReceipt(artifactReceipt{
		Extension: desired.Extension, Version: desired.Version, Revision: desired.Revision,
		ArtifactSHA256:  desired.Artifact.SHA256,
		PackageRevision: installed.Manifest.Descriptor.Identity.Revision,
	}); err != nil {
		return store.Installed{}, err
	}
	return installed, nil
}

func (r *ArtifactReconciler) verifyInstalled(installed store.Installed, desired DesiredExtension) error {
	identity := installed.Manifest.Descriptor.Identity
	if identity.ID != desired.Extension || identity.Version != desired.Version {
		return errors.New("installed extension identity or version does not match desired state")
	}
	if desired.Digest != "" {
		digest, err := installed.Digest()
		if err != nil {
			return err
		}
		if digest != desired.Digest {
			return errors.New("installed extension digest does not match desired state")
		}
	}
	return nil
}

func (r *ArtifactReconciler) downloadAndExtract(ctx context.Context, artifact DesiredArtifact) (string, func(), error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return "", func() {}, err
	}
	response, err := r.artifacts.Do(request)
	if err != nil {
		return "", func() {}, fmt.Errorf("download extension artifact: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", func() {}, fmt.Errorf("download extension artifact: origin returned %s", response.Status)
	}
	if err := os.MkdirAll(r.storeDir, 0o700); err != nil {
		return "", func() {}, err
	}
	temporary, err := os.MkdirTemp(r.storeDir, ".artifact-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(temporary) }
	archivePath := filepath.Join(temporary, "package.tar.gz")
	archive, err := os.OpenFile(archivePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(archive, hash), io.LimitReader(response.Body, maxExtensionArchiveBytes+1))
	closeErr := archive.Close()
	if copyErr != nil || closeErr != nil {
		cleanup()
		return "", func() {}, errors.Join(copyErr, closeErr)
	}
	if written > maxExtensionArchiveBytes {
		cleanup()
		return "", func() {}, errors.New("extension artifact exceeds size limit")
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		cleanup()
		return "", func() {}, errors.New("extension artifact SHA-256 does not match desired state")
	}
	extracted := filepath.Join(temporary, "extracted")
	if err := extractExtensionArchive(archivePath, extracted); err != nil {
		cleanup()
		return "", func() {}, err
	}
	root, err := extensionArchiveRoot(extracted)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return root, cleanup, nil
}

func extractExtensionArchive(archivePath, destination string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return errors.New("extension artifact is not a gzip-compressed tar archive")
	}
	defer compressed.Close()
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	reader := tar.NewReader(compressed)
	files := 0
	var bytes int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if name == "." || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return errors.New("extension artifact contains an unsafe path")
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if target != destination && !strings.HasPrefix(target, destination+string(filepath.Separator)) {
			return errors.New("extension artifact escapes its staging directory")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			files++
			bytes += header.Size
			if files > maxExtensionArchiveFiles || header.Size < 0 || bytes > maxExtensionArchiveBytes {
				return errors.New("extension artifact exceeds extraction limits")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				return errors.Join(copyErr, closeErr)
			}
		default:
			return errors.New("extension artifact contains unsupported links or special files")
		}
	}
	return nil
}

func extensionArchiveRoot(extracted string) (string, error) {
	if _, err := os.Stat(filepath.Join(extracted, store.ManifestName)); err == nil {
		return extracted, nil
	}
	entries, err := os.ReadDir(extracted)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return "", errors.New("extension artifact must contain one package root")
	}
	root := filepath.Join(extracted, entries[0].Name())
	if _, err := os.Stat(filepath.Join(root, store.ManifestName)); err != nil {
		return "", errors.New("extension artifact is missing plugin.json")
	}
	return root, nil
}

func (r *ArtifactReconciler) receiptPath(extension string) string {
	digest := sha256.Sum256([]byte(extension))
	return filepath.Join(r.storeDir, ".desired", hex.EncodeToString(digest[:])+".json")
}

func (r *ArtifactReconciler) readReceipt(extension string) (artifactReceipt, error) {
	var receipt artifactReceipt
	data, err := os.ReadFile(r.receiptPath(extension))
	if err != nil {
		return receipt, err
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func (r *ArtifactReconciler) writeReceipt(receipt artifactReceipt) error {
	directory := filepath.Dir(r.receiptPath(receipt.Extension))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".receipt-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, r.receiptPath(receipt.Extension))
}
