// Package extensions reconciles Planner-owned extension desired state with
// Spinner's role-specific extension hosts.
package extensions

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
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
	integrationextensions "github.com/webong/gateway/pkg/spinner/extensions/integration"
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
	Extension           string          `json:"extension"`
	Version             string          `json:"version"`
	Digest              string          `json:"digest"`
	Artifact            DesiredArtifact `json:"artifact"`
	Configuration       json.RawMessage `json:"configuration,omitempty"`
	ConfigurationSHA256 string          `json:"configuration_sha256"`
	Revision            int             `json:"revision"`
}

type reconciledExtension struct {
	desired   DesiredExtension
	installed store.Installed
}

// Registries are the role-specific hosts updated by one desired-state pass.
// A nil role uses the process default for that role.
type Registries struct {
	Automation  *automationextensions.Registry
	Integration *integrationextensions.Registry
}

func (r Registries) withDefaults() Registries {
	if r.Automation == nil {
		r.Automation = automationextensions.Runtime()
	}
	if r.Integration == nil {
		r.Integration = integrationextensions.DefaultRegistry
	}
	return r
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
	registries Registries

	mu     sync.Mutex
	loaded map[string]string
}

// StartArtifactReconciliation keeps one running host aligned with
// Planner-owned extension desired state. Reconciliation failures close the
// affected external extensions and are reported without stopping ingress.
func StartArtifactReconciliation(
	ctx context.Context,
	plannerURL, token, storeDir string,
	plannerClient, artifactClient interface {
		Do(*http.Request) (*http.Response, error)
	},
	registries Registries,
	interval time.Duration,
	logError func(string, ...any),
) error {
	reconciler, err := NewArtifactReconciler(
		plannerURL, token, storeDir, plannerClient, artifactClient, registries,
	)
	if err != nil {
		return err
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if logError == nil {
		logError = func(string, ...any) {}
	}
	reconcile := func() {
		if err := reconciler.Reconcile(ctx); err != nil && ctx.Err() == nil {
			logError("Extension artifact reconciliation failed; unavailable extensions remain closed: %v", err)
		}
	}
	// Resolve the starting catalog before the host advertises readiness. A
	// failed desired-state read still fails closed and is retried below.
	reconcile()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reconcile()
			}
		}
	}()
	return nil
}

func NewArtifactReconciler(
	plannerURL, token, storeDir string,
	plannerClient, artifactClient interface {
		Do(*http.Request) (*http.Response, error)
	},
	registries Registries,
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
	registries = registries.withDefaults()
	return &ArtifactReconciler{
		plannerURL: strings.TrimRight(base.String(), "/"), token: token, storeDir: storeDir,
		planner: plannerClient, artifacts: artifactClient, registries: registries, loaded: map[string]string{},
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
	selected := make(map[plugin.Identity]reconciledExtension, len(desired))
	manifests := make([]packagekit.Manifest, 0, len(desired))
	for _, extension := range desired {
		desiredIDs[extension.Extension] = true
		installed, err := r.materialize(ctx, packages, extension)
		if err != nil {
			r.closeExternalExtensions()
			return fmt.Errorf("reconcile extension %s: %w", extension.Extension, err)
		}
		selected[installed.Manifest.Descriptor.Identity] = reconciledExtension{desired: extension, installed: installed}
		manifests = append(manifests, installed.Manifest)
	}
	plan, err := packagekit.Resolve(manifests)
	if err != nil {
		r.closeExternalExtensions()
		return fmt.Errorf("resolve extension dependencies: %w", err)
	}
	if err := validateDependencyPlan(plan, selected); err != nil {
		r.closeExternalExtensions()
		return err
	}
	for _, identity := range plan.Order {
		selection, ok := selected[identity]
		if !ok {
			r.closeExternalExtensions()
			return errors.New("extension dependency plan selected an unknown package")
		}
		extension := selection.desired
		installed := selection.installed
		bindings := dependencyBindings(plan.Bindings, identity)
		loadedRevision := fmt.Sprintf("%d:%s:%s:%s", extension.Revision, extension.Artifact.SHA256, extension.ConfigurationSHA256, installed.Manifest.Descriptor.Identity.Revision)
		loadedRevision += ":" + dependencyRevision(bindings)
		if r.loaded[extension.Extension] == loadedRevision {
			continue
		}
		if err := automationextensions.LoadExternalInstalled(ctx, r.registries.Automation, installed); err != nil {
			r.registries.Automation.UnregisterExtension(extension.Extension)
			r.registries.Integration.Unregister(extension.Extension)
			delete(r.loaded, extension.Extension)
			return fmt.Errorf("launch Spinner entrypoint: %w", err)
		}
		if err := integrationextensions.LoadExternalInstalledWithDependencies(ctx, r.registries.Integration, installed, extension.Configuration, bindings); err != nil {
			r.registries.Automation.UnregisterExtension(extension.Extension)
			r.registries.Integration.Unregister(extension.Extension)
			delete(r.loaded, extension.Extension)
			return fmt.Errorf("launch Spinner integration entrypoint for %s: %w", extension.Extension, err)
		}
		r.loaded[extension.Extension] = loadedRevision
	}
	for _, extension := range r.registries.Automation.ExternalExtensions() {
		if desiredIDs[extension] {
			continue
		}
		r.registries.Automation.UnregisterExtension(extension)
		r.registries.Integration.Unregister(extension)
		delete(r.loaded, extension)
	}
	for _, extension := range r.registries.Integration.ExternalExtensions() {
		if desiredIDs[extension] {
			continue
		}
		r.registries.Integration.Unregister(extension)
		delete(r.loaded, extension)
	}
	return nil
}

func dependencyBindings(bindings []packagekit.Resolution, consumer plugin.Identity) []packagekit.Resolution {
	result := make([]packagekit.Resolution, 0)
	for _, binding := range bindings {
		if binding.Consumer == consumer {
			result = append(result, binding)
		}
	}
	return result
}

func dependencyRevision(bindings []packagekit.Resolution) string {
	encoded, _ := json.Marshal(bindings)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validateDependencyPlan(plan packagekit.Plan, selected map[plugin.Identity]reconciledExtension) error {
	for _, binding := range plan.Bindings {
		consumer, ok := selected[binding.Consumer]
		if !ok {
			return errors.New("extension dependency plan names an unknown consumer")
		}
		if _, err := consumer.installed.Manifest.Descriptor.LookupReverse(binding.Requirement.Contract, binding.Requirement.Operation); err != nil {
			return fmt.Errorf("extension %s requires %s@%s/%s without declaring the reverse capability", binding.Consumer.ID, binding.Requirement.Contract.Name, binding.Requirement.Contract.Version, binding.Requirement.Operation)
		}
		provider, ok := selected[binding.Provider]
		if !ok || !integrationextensions.Supports(provider.installed.Manifest.Descriptor) {
			return fmt.Errorf("extension %s dependency %s@%s/%s is not supplied by a Spinner integration", binding.Consumer.ID, binding.Requirement.Contract.Name, binding.Requirement.Contract.Version, binding.Requirement.Operation)
		}
	}
	return nil
}

func (r *ArtifactReconciler) closeExternalExtensions() {
	for _, extension := range r.registries.Automation.ExternalExtensions() {
		r.registries.Automation.UnregisterExtension(extension)
		delete(r.loaded, extension)
	}
	for _, extension := range r.registries.Integration.ExternalExtensions() {
		r.registries.Integration.Unregister(extension)
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
	return err == nil && validExtensionConfiguration(extension.Configuration) && validSHA256(extension.ConfigurationSHA256) &&
		extension.ConfigurationSHA256 == digestConfiguration(extension.Configuration) && extension.Extension != "" && extension.Version != "" && extension.Revision > 0 &&
		artifact.Scheme == "https" && artifact.Host != "" && artifact.User == nil && artifact.Fragment == "" &&
		validSHA256(extension.Artifact.SHA256) &&
		(extension.Digest == "" || validSHA256(extension.Digest))
}

func digestConfiguration(configuration json.RawMessage) string {
	digest := sha256.Sum256(configuration)
	return hex.EncodeToString(digest[:])
}

func validExtensionConfiguration(configuration json.RawMessage) bool {
	if len(configuration) == 0 {
		return true
	}
	if len(configuration) > 65536 || !json.Valid(configuration) || !strings.HasPrefix(strings.TrimSpace(string(configuration)), "{") {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(configuration, &object) == nil && object != nil
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
			receipt.ArtifactSHA256 == desired.Artifact.SHA256 &&
			receipt.PackageRevision == installed.Manifest.Descriptor.Identity.Revision {
			if err := r.verifyInstalled(installed, desired); err != nil {
				return store.Installed{}, err
			}
			if receipt.Revision != desired.Revision {
				receipt.Revision = desired.Revision
				if err := r.writeReceipt(receipt); err != nil {
					return store.Installed{}, err
				}
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
		if name == "." && header.Typeflag == tar.TypeDir {
			continue
		}
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
