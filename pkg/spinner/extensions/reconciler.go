// Package extensions reconciles Planner-owned extension desired state with
// Spinner's role-specific extension hosts.
package extensions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/ext-go/engine"
	"github.com/webong/ext/pkg/ext-go/engine/host/store"
	packagekit "github.com/webong/ext/pkg/ext-go/engine/package"
	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
	integrationextensions "github.com/webong/gateway/pkg/spinner/extensions/integration"
)

const (
	maxExtensionArchiveBytes = 512 << 20
	maxExtensionArchiveFiles = 250000
)

// Keep Gateway's pre-migration archive and expanded-tree budgets.
func extensionStoreLimits() store.Limits {
	return store.Limits{
		MaxArchiveBytes:  maxExtensionArchiveBytes,
		MaxArtifactBytes: maxExtensionArchiveBytes,
		MaxTreeBytes:     maxExtensionArchiveBytes,
		MaxTreeFiles:     maxExtensionArchiveFiles,
	}
}

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
	// Resolve the starting catalog before the host advertises readiness. A
	// failed desired-state read still fails closed and is retried below.
	reconciler.ReconcileAndLog(ctx, logError)
	go reconciler.Run(ctx, interval, logError)
	return nil
}

// ReconcileAndLog performs one pass, preserving the fail-closed behavior on a
// temporary Planner error while allowing a later pass to recover.
func (r *ArtifactReconciler) ReconcileAndLog(ctx context.Context, logError func(string, ...any)) {
	if err := r.Reconcile(ctx); err != nil && ctx.Err() == nil && logError != nil {
		logError("Extension artifact reconciliation failed; unavailable extensions remain closed: %v", err)
	}
}

// Run owns the retry loop until the host cancels it. Call ReconcileAndLog
// before advertising readiness when the initial catalog is required.
func (r *ArtifactReconciler) Run(ctx context.Context, interval time.Duration, logError func(string, ...any)) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.ReconcileAndLog(ctx, logError)
		}
	}
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
		artifactClient = NewArtifactHTTPClient()
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
	packages, err := store.New(r.storeDir, extensionStoreLimits())
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

func (r *ArtifactReconciler) materialize(ctx context.Context, packages *store.Store, desired DesiredExtension) (store.Installed, error) {
	// A damaged installed package is not repaired or silently replaced.
	if _, err := packages.Load(desired.Extension); err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Installed{}, err
	}
	// Bound the transfer even when an injected HTTP client has no timeout.
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	installed, err := packages.InstallArchive(ctx, store.Archive{
		Source: desired.Artifact.URL, SHA256: desired.Artifact.SHA256, ID: desired.Extension,
	}, store.ArchiveOptions{Client: r.artifacts})
	if err != nil {
		return store.Installed{}, err
	}
	if err := r.verifyInstalled(installed, desired); err != nil {
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
