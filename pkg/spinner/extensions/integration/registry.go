package integrationextensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
	integrationcontract "github.com/webong/gateway/arranger/integration"
	companionextensions "github.com/webong/gateway/pkg/spinner/extensions/companion"
	extensionhost "github.com/webong/gateway/pkg/spinner/extensions/internal/host"
)

type mounted struct {
	identity plugin.Identity
	process  extensionProcess
	http     []httpMount
	private  []httpMount
	adapters map[string]struct{}
	jobs     map[string]integrationcontract.RuntimeJob
	failures map[string]struct{}
}

type extensionProcess interface {
	Descriptor() plugin.Descriptor
	Call(context.Context, string, any, any) error
	Close() error
}

type httpMount struct {
	prefix string
	proxy  http.Handler
}

// Registry owns live process sessions. At most one active extension may claim
// the public HTTP data endpoint; ambiguity fails closed.
type Registry struct {
	mu      sync.RWMutex
	mounted map[string]mounted
}

func NewRegistry() *Registry { return &Registry{mounted: map[string]mounted{}} }

var DefaultRegistry = NewRegistry()

// RegisterHTTP installs an already-hosted adapter without importing it into
// Spinner. It is useful to embedders and tests; production artifact loading
// uses LoadExternalInstalled and therefore retains process isolation.
func (r *Registry) RegisterHTTP(identity string, handler http.Handler) error {
	return r.RegisterHTTPMount(identity, "/", handler)
}

// RegisterHTTPMount installs an already-hosted, prefix-scoped adapter without
// importing its implementation into Spinner.
func (r *Registry) RegisterHTTPMount(identity, prefix string, handler http.Handler) error {
	if handler == nil || (plugin.Identity{ID: identity, Revision: "static"}).Validate() != nil {
		return plugin.ErrInvalid
	}
	prefix, err := validHTTPPrefix(prefix)
	if err != nil {
		return err
	}
	r.register(identity, mounted{http: []httpMount{{prefix: prefix, proxy: handler}}})
	return nil
}

func Supports(descriptor plugin.Descriptor) bool {
	_, err := descriptor.Lookup(integrationcontract.RuntimeContract, integrationcontract.OperationStart)
	return err == nil
}

func LoadExternalInstalled(ctx context.Context, registry *Registry, inst store.Installed) error {
	return LoadExternalInstalledWithConfiguration(ctx, registry, inst, nil)
}

func LoadExternalInstalledWithConfiguration(ctx context.Context, registry *Registry, inst store.Installed, configuration json.RawMessage) error {
	return LoadExternalInstalledWithDependencies(ctx, registry, inst, configuration, nil)
}

// LoadExternalInstalledWithDependencies starts one verified Integration and
// grants it only the exact provider bindings selected by packagekit.Resolve.
// Dependencies are routed through the host; consumers never receive another
// extension's process handle or credentials.
func LoadExternalInstalledWithDependencies(ctx context.Context, registry *Registry, inst store.Installed, configuration json.RawMessage, bindings []packagekit.Resolution) error {
	if !Supports(inst.Manifest.Descriptor) {
		return nil
	}
	if registry == nil {
		registry = DefaultRegistry
	}
	launch, err := extensionhost.SpinnerLaunch(inst)
	if err != nil {
		return fmt.Errorf("spinner entrypoint is invalid: %w", err)
	}
	proc, err := host.Open(ctx, inst, host.Options{
		Reverse: registry.DependencyReverseOptions(
			inst.Manifest.Descriptor.Identity,
			bindings,
			DefaultHost.ReverseOptionsFor(inst.Manifest.Descriptor.Identity, companionextensions.DefaultHost.ReverseOptions()),
		),
		Launch: launch,
	})
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	var advertisement integrationcontract.RuntimeStartResponse
	err = proc.Call(callCtx, plugin.Method(integrationcontract.RuntimeContract, integrationcontract.OperationStart), DefaultHost.StartRequest(inst.Manifest.Descriptor.Identity, configuration), &advertisement)
	cancel()
	if err != nil {
		_ = proc.Close()
		if diagnostic := strings.TrimSpace(proc.Stderr()); diagnostic != "" {
			return fmt.Errorf("start Spinner integration: %w (guest: %s)", err, diagnostic)
		}
		return fmt.Errorf("start Spinner integration: %w", err)
	}
	mounts := make([]httpMount, 0, len(advertisement.HTTP))
	privateMounts := make([]httpMount, 0, len(advertisement.PrivateHTTP))
	seenPrefixes := make(map[string]bool, len(advertisement.HTTP))
	buildMounts := func(advertisedMounts []integrationcontract.HTTPMount, destination *[]httpMount) error {
		for _, advertised := range advertisedMounts {
			prefix, err := validHTTPPrefix(advertised.Prefix)
			if err != nil || seenPrefixes[prefix] {
				return errors.New("Spinner integration advertised an invalid or duplicate HTTP prefix")
			}
			seenPrefixes[prefix] = true
			target, err := url.Parse(advertised.Address)
			if err != nil || target.Scheme != "http" || target.User != nil || target.Path != "" || target.RawQuery != "" || target.Fragment != "" || !loopback(target.Host) {
				return errors.New("Spinner integration advertised an invalid private HTTP endpoint")
			}
			reverseProxy := httputil.NewSingleHostReverseProxy(target)
			reverseProxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
				http.Error(writer, "Gateway integration is unavailable", http.StatusBadGateway)
			}
			*destination = append(*destination, httpMount{prefix: prefix, proxy: reverseProxy})
		}
		return nil
	}
	if err := buildMounts(advertisement.HTTP, &mounts); err != nil {
		_ = proc.Close()
		return err
	}
	seenPrefixes = make(map[string]bool, len(advertisement.PrivateHTTP))
	if err := buildMounts(advertisement.PrivateHTTP, &privateMounts); err != nil {
		_ = proc.Close()
		return err
	}
	adapters := make(map[string]struct{}, len(advertisement.DeliveryAdapters))
	for _, name := range advertisement.DeliveryAdapters {
		if !integrationcontract.ValidAdapterName(name) || strings.HasPrefix(name, "gateway.") {
			_ = proc.Close()
			return errors.New("Spinner integration advertised an invalid delivery adapter")
		}
		if _, duplicate := adapters[name]; duplicate {
			_ = proc.Close()
			return errors.New("Spinner integration advertised a duplicate delivery adapter")
		}
		adapters[name] = struct{}{}
	}
	jobs := make(map[string]integrationcontract.RuntimeJob, len(advertisement.Jobs))
	seenJobNames := make(map[string]struct{}, len(advertisement.Jobs))
	for _, job := range advertisement.Jobs {
		language := string(job.Language)
		if !integrationcontract.ValidAdapterName(job.Name) || strings.HasPrefix(job.Name, "gateway.") ||
			language != "native."+job.Name {
			_ = proc.Close()
			return errors.New("Spinner integration advertised an invalid native job")
		}
		if _, duplicate := jobs[language]; duplicate {
			_ = proc.Close()
			return errors.New("Spinner integration advertised a duplicate native job language")
		}
		if _, duplicate := seenJobNames[job.Name]; duplicate {
			_ = proc.Close()
			return errors.New("Spinner integration advertised a duplicate native job name")
		}
		jobs[language] = job
		seenJobNames[job.Name] = struct{}{}
	}
	failures := make(map[string]struct{}, len(advertisement.DeliveryFailureHandlers))
	if len(advertisement.DeliveryFailureHandlers) > 0 {
		if _, err := proc.Descriptor().Lookup(integrationcontract.DeliveryFailureContract, integrationcontract.OperationDeliveryFailure); err != nil {
			_ = proc.Close()
			return errors.New("Spinner integration advertised a failure handler without implementing its contract")
		}
	}
	for _, name := range advertisement.DeliveryFailureHandlers {
		if !integrationcontract.ValidAdapterName(name) || strings.HasPrefix(name, "gateway.") {
			_ = proc.Close()
			return errors.New("Spinner integration advertised an invalid delivery failure handler")
		}
		if _, duplicate := failures[name]; duplicate {
			_ = proc.Close()
			return errors.New("Spinner integration advertised a duplicate delivery failure handler")
		}
		failures[name] = struct{}{}
	}
	registry.register(inst.Manifest.Descriptor.Identity.ID, mounted{
		identity: inst.Manifest.Descriptor.Identity,
		process:  proc,
		http:     mounts,
		private:  privateMounts,
		adapters: adapters,
		jobs:     jobs,
		failures: failures,
	})
	return nil
}

// ResolveDeliveryFailureHandler returns a late-bound failure hook backed by
// exactly one installed Extension. Missing and ambiguous providers fail closed.
func (r *Registry) ResolveDeliveryFailureHandler(adapter string) (integrationcontract.DeliveryFailureHandler, bool) {
	if _, ok := r.failureProcess(adapter); !ok {
		return nil, false
	}
	return extensionDeliveryFailureHandler{registry: r, adapter: adapter}, true
}

func (r *Registry) failureProcess(adapter string) (extensionProcess, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var selected extensionProcess
	for _, extension := range r.mounted {
		if _, ok := extension.failures[adapter]; !ok || extension.process == nil {
			continue
		}
		if selected != nil {
			return nil, false
		}
		selected = extension.process
	}
	return selected, selected != nil
}

type extensionDeliveryFailureHandler struct {
	registry *Registry
	adapter  string
}

func (handler extensionDeliveryFailureHandler) FollowUp(ctx context.Context, request integrationcontract.DeliveryFailureRequest) (*integrationcontract.GatewayDelivery, error) {
	process, ok := handler.registry.failureProcess(handler.adapter)
	if !ok {
		return nil, errors.New("delivery failure Extension is unavailable")
	}
	var response integrationcontract.DeliveryFailureResponse
	if err := process.Call(ctx, plugin.Method(integrationcontract.DeliveryFailureContract, integrationcontract.OperationDeliveryFailure), request, &response); err != nil {
		return nil, err
	}
	if response.FollowUp != nil {
		if err := response.FollowUp.Validate(); err != nil {
			return nil, errors.New("delivery failure Extension returned an invalid follow-up")
		}
	}
	return response.FollowUp, nil
}

// NativeJobNames reports only names with one live provider. Planner uses this
// list while claiming schedules, so unavailable jobs are never advertised.
func (r *Registry) NativeJobNames() []string {
	r.mu.RLock()
	counts := make(map[string]int)
	for _, extension := range r.mounted {
		for _, job := range extension.jobs {
			counts[job.Name]++
		}
	}
	r.mu.RUnlock()
	result := make([]string, 0, len(counts))
	for name, count := range counts {
		if count == 1 {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

// RunNativeJob routes one claimed schedule to exactly one live Extension.
// Missing and ambiguous providers fail closed.
func (r *Registry) RunNativeJob(ctx context.Context, language string, source string, payload map[string]any) error {
	process, job, ok := r.jobProcess(language)
	if !ok {
		return errors.New("native job Extension is unavailable")
	}
	var response struct{}
	return process.Call(ctx, plugin.Method(integrationcontract.JobContract, integrationcontract.OperationRunJob),
		integrationcontract.JobRequest{Name: job.Name, Source: source, Payload: payload}, &response)
}

func (r *Registry) jobProcess(language string) (extensionProcess, integrationcontract.RuntimeJob, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var selected extensionProcess
	var selectedJob integrationcontract.RuntimeJob
	for _, extension := range r.mounted {
		job, ok := extension.jobs[language]
		if !ok || extension.process == nil {
			continue
		}
		if selected != nil {
			return nil, integrationcontract.RuntimeJob{}, false
		}
		selected, selectedJob = extension.process, job
	}
	return selected, selectedJob, selected != nil
}

// ResolveDeliveryAdapter returns a late-bound adapter backed by exactly one
// installed Extension. Missing and ambiguous providers fail closed.
func (r *Registry) ResolveDeliveryAdapter(name string) (integrationcontract.GatewayDeliveryAdapter, bool) {
	if _, ok := r.adapterProcess(name); !ok {
		return nil, false
	}
	return extensionDeliveryAdapter{registry: r, name: name}, true
}

// DeliveryAdapterNames reports only unambiguous live adapter names so shared
// delivery workers never claim work they cannot currently execute.
func (r *Registry) DeliveryAdapterNames() []string {
	r.mu.RLock()
	counts := make(map[string]int)
	for _, extension := range r.mounted {
		for name := range extension.adapters {
			counts[name]++
		}
	}
	r.mu.RUnlock()
	result := make([]string, 0, len(counts))
	for name, count := range counts {
		if count == 1 {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func (r *Registry) adapterProcess(name string) (extensionProcess, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var selected extensionProcess
	for _, extension := range r.mounted {
		if _, ok := extension.adapters[name]; !ok || extension.process == nil {
			continue
		}
		if selected != nil {
			return nil, false
		}
		selected = extension.process
	}
	return selected, selected != nil
}

type extensionDeliveryAdapter struct {
	registry *Registry
	name     string
}

func (adapter extensionDeliveryAdapter) DeliverGateway(ctx context.Context, delivery integrationcontract.GatewayDelivery) error {
	_, err := adapter.DeliverGatewayResult(ctx, delivery)
	return err
}

func (adapter extensionDeliveryAdapter) DeliverGatewayResult(ctx context.Context, delivery integrationcontract.GatewayDelivery) (integrationcontract.GatewayDeliveryResult, error) {
	process, ok := adapter.registry.adapterProcess(adapter.name)
	if !ok {
		return integrationcontract.GatewayDeliveryResult{}, errors.New("delivery adapter Extension is unavailable")
	}
	var response integrationcontract.DeliveryAdapterResponse
	err := process.Call(ctx, plugin.Method(integrationcontract.DeliveryAdapterContract, integrationcontract.OperationDeliverAdapter),
		integrationcontract.DeliveryAdapterRequest{Adapter: adapter.name, Delivery: delivery}, &response)
	if err != nil {
		return integrationcontract.GatewayDeliveryResult{}, err
	}
	switch response.Failure {
	case "":
		return response.Result, nil
	case integrationcontract.AdapterFailurePermanent:
		return response.Result, integrationcontract.ErrPermanentDelivery
	case integrationcontract.AdapterFailureUncertain:
		return response.Result, integrationcontract.ErrUncertainDelivery
	case integrationcontract.AdapterFailureTransient:
		return response.Result, errors.New("delivery adapter reported a transient failure")
	default:
		return integrationcontract.GatewayDeliveryResult{}, errors.New("delivery adapter returned an invalid failure classification")
	}
}

// DependencyReverseOptions exposes only dependency operations resolved for one
// immutable consumer identity. A missing, replaced, or incompatible provider
// fails closed; the fallback retains host-owned Arranger capabilities.
func (r *Registry) DependencyReverseOptions(consumer plugin.Identity, bindings []packagekit.Resolution, fallback *plugin.ReverseOptions) *plugin.ReverseOptions {
	resolved := make(map[dependencyOperation]plugin.Identity)
	for _, binding := range bindings {
		if binding.Consumer != consumer {
			continue
		}
		resolved[dependencyOperation{binding.Requirement.Contract, binding.Requirement.Operation}] = binding.Provider
	}
	return &plugin.ReverseOptions{
		Authorize: func(ctx context.Context, request plugin.Request) error {
			provider, ok := resolved[dependencyOperation{request.Contract, request.Operation}]
			if ok {
				return r.authorizeDependency(provider, request)
			}
			if fallback == nil || fallback.Authorize == nil {
				return plugin.ErrDenied
			}
			return fallback.Authorize(ctx, request)
		},
		Handle: func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
			provider, ok := resolved[dependencyOperation{request.Contract, request.Operation}]
			if ok {
				return r.callDependency(ctx, provider, request)
			}
			if fallback == nil || fallback.Handle == nil {
				return nil, plugin.ErrDenied
			}
			return fallback.Handle(ctx, request)
		},
		Timeout:         5 * time.Minute,
		MaxPayloadBytes: plugin.MaxFrameBytes - (64 << 10),
		MaxInFlight:     16,
	}
}

type dependencyOperation struct {
	contract  plugin.ContractRef
	operation string
}

func (r *Registry) authorizeDependency(provider plugin.Identity, request plugin.Request) error {
	process, err := r.dependencyProcess(provider)
	if err != nil {
		return err
	}
	if _, err := process.Descriptor().Lookup(request.Contract, request.Operation); err != nil {
		return plugin.ErrMismatch
	}
	return nil
}

func (r *Registry) callDependency(ctx context.Context, provider plugin.Identity, request plugin.Request) (json.RawMessage, error) {
	process, err := r.dependencyProcess(provider)
	if err != nil {
		return nil, err
	}
	if _, err := process.Descriptor().Lookup(request.Contract, request.Operation); err != nil {
		return nil, plugin.ErrMismatch
	}
	var response json.RawMessage
	if err := process.Call(ctx, plugin.Method(request.Contract, request.Operation), request.Payload, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func (r *Registry) dependencyProcess(identity plugin.Identity) (extensionProcess, error) {
	r.mu.RLock()
	mounted, ok := r.mounted[identity.ID]
	r.mu.RUnlock()
	if !ok || mounted.process == nil {
		return nil, plugin.ErrNotFound
	}
	if mounted.identity != identity {
		return nil, plugin.ErrMismatch
	}
	return mounted.process, nil
}

func validHTTPPrefix(prefix string) (string, error) {
	if prefix == "" || !strings.HasPrefix(prefix, "/") || strings.ContainsAny(prefix, "?#") || path.Clean(prefix) != prefix || strings.HasPrefix(prefix, "/_internal") {
		return "", plugin.ErrInvalid
	}
	return prefix, nil
}

func loopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (r *Registry) register(identity string, next mounted) {
	r.mu.Lock()
	previous, exists := r.mounted[identity]
	r.mounted[identity] = next
	r.mu.Unlock()
	if exists && previous.process != nil {
		_ = previous.process.Close()
	}
}

func (r *Registry) Unregister(identity string) {
	r.mu.Lock()
	previous, exists := r.mounted[identity]
	delete(r.mounted, identity)
	r.mu.Unlock()
	if exists && previous.process != nil {
		_ = previous.process.Close()
	}
}

func (r *Registry) ExternalExtensions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]string, 0, len(r.mounted))
	for identity := range r.mounted {
		result = append(result, identity)
	}
	return result
}

func (r *Registry) Handler() http.Handler {
	return r.handler(false)
}

// PrivateHandler exposes only Extension mounts advertised for Spinner's
// authenticated admin listener. Public mounts can never be reached through it.
func (r *Registry) PrivateHandler() http.Handler {
	return r.handler(true)
}

func (r *Registry) handler(private bool) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		r.mu.RLock()
		var selected http.Handler
		selectedLength := -1
		ambiguous := false
		for _, extension := range r.mounted {
			mounts := extension.http
			if private {
				mounts = extension.private
			}
			for _, mount := range mounts {
				if !pathMatches(request.URL.Path, mount.prefix) {
					continue
				}
				if len(mount.prefix) > selectedLength {
					selected, selectedLength, ambiguous = mount.proxy, len(mount.prefix), false
				} else if len(mount.prefix) == selectedLength {
					ambiguous = true
				}
			}
		}
		r.mu.RUnlock()
		if selected == nil {
			http.Error(writer, "Gateway extension for this route is not installed", http.StatusServiceUnavailable)
			return
		}
		if ambiguous {
			http.Error(writer, "Gateway extension route selection is ambiguous", http.StatusServiceUnavailable)
			return
		}
		selected.ServeHTTP(writer, request)
	})
}

func pathMatches(requestPath, prefix string) bool {
	return prefix == "/" || requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/")
}
