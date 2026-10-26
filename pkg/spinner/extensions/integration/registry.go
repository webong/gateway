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
			DefaultHost.ReverseOptions(companionextensions.DefaultHost.ReverseOptions()),
		),
		Launch: launch,
	})
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	var advertisement integrationcontract.RuntimeStartResponse
	err = proc.Call(callCtx, plugin.Method(integrationcontract.RuntimeContract, integrationcontract.OperationStart), DefaultHost.StartRequest(configuration), &advertisement)
	cancel()
	if err != nil {
		_ = proc.Close()
		if diagnostic := strings.TrimSpace(proc.Stderr()); diagnostic != "" {
			return fmt.Errorf("start Spinner integration: %w (guest: %s)", err, diagnostic)
		}
		return fmt.Errorf("start Spinner integration: %w", err)
	}
	mounts := make([]httpMount, 0, len(advertisement.HTTP))
	seenPrefixes := make(map[string]bool, len(advertisement.HTTP))
	for _, advertised := range advertisement.HTTP {
		prefix, err := validHTTPPrefix(advertised.Prefix)
		if err != nil || seenPrefixes[prefix] {
			_ = proc.Close()
			return errors.New("Spinner integration advertised an invalid or duplicate HTTP prefix")
		}
		seenPrefixes[prefix] = true
		target, err := url.Parse(advertised.Address)
		if err != nil || target.Scheme != "http" || target.User != nil || target.Path != "" || target.RawQuery != "" || target.Fragment != "" || !loopback(target.Host) {
			_ = proc.Close()
			return errors.New("Spinner integration advertised an invalid private HTTP endpoint")
		}
		reverseProxy := httputil.NewSingleHostReverseProxy(target)
		reverseProxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(writer, "Gateway integration is unavailable", http.StatusBadGateway)
		}
		mounts = append(mounts, httpMount{prefix: prefix, proxy: reverseProxy})
	}
	registry.register(inst.Manifest.Descriptor.Identity.ID, mounted{
		identity: inst.Manifest.Descriptor.Identity,
		process:  proc,
		http:     mounts,
	})
	return nil
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
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		r.mu.RLock()
		var selected http.Handler
		selectedLength := -1
		ambiguous := false
		for _, extension := range r.mounted {
			for _, mount := range extension.http {
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
