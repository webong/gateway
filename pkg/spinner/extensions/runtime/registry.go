package runtimeextensions

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
	runtimecontract "github.com/webong/gateway/arranger/integration"
	companionextensions "github.com/webong/gateway/pkg/spinner/extensions/companion"
	"github.com/webong/gateway/pkg/spinner/extensions/packagehost"
)

type mounted struct {
	process *host.Process
	http    []httpMount
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
	_, err := descriptor.Lookup(runtimecontract.RuntimeContract, runtimecontract.OperationStart)
	return err == nil
}

func LoadExternalInstalled(ctx context.Context, registry *Registry, inst store.Installed) error {
	return LoadExternalInstalledWithConfiguration(ctx, registry, inst, nil)
}

func LoadExternalInstalledWithConfiguration(ctx context.Context, registry *Registry, inst store.Installed, configuration json.RawMessage) error {
	if !Supports(inst.Manifest.Descriptor) {
		return nil
	}
	if registry == nil {
		registry = DefaultRegistry
	}
	launch, err := packagehost.SpinnerLaunch(inst)
	if err != nil {
		return fmt.Errorf("spinner entrypoint is invalid: %w", err)
	}
	proc, err := host.Open(ctx, inst, host.Options{
		Reverse: DefaultHost.ReverseOptions(companionextensions.DefaultHost.ReverseOptions()),
		Launch:  launch,
	})
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	var advertisement runtimecontract.RuntimeStartResponse
	err = proc.Call(callCtx, plugin.Method(runtimecontract.RuntimeContract, runtimecontract.OperationStart), DefaultHost.StartRequest(configuration), &advertisement)
	cancel()
	if err != nil {
		_ = proc.Close()
		return fmt.Errorf("start Spinner runtime: %w", err)
	}
	mounts := make([]httpMount, 0, len(advertisement.HTTP))
	seenPrefixes := make(map[string]bool, len(advertisement.HTTP))
	for _, advertised := range advertisement.HTTP {
		prefix, err := validHTTPPrefix(advertised.Prefix)
		if err != nil || seenPrefixes[prefix] {
			_ = proc.Close()
			return errors.New("Spinner runtime advertised an invalid or duplicate HTTP prefix")
		}
		seenPrefixes[prefix] = true
		target, err := url.Parse(advertised.Address)
		if err != nil || target.Scheme != "http" || target.User != nil || target.Path != "" || target.RawQuery != "" || target.Fragment != "" || !loopback(target.Host) {
			_ = proc.Close()
			return errors.New("Spinner runtime advertised an invalid private HTTP endpoint")
		}
		reverseProxy := httputil.NewSingleHostReverseProxy(target)
		reverseProxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(writer, "Gateway extension runtime is unavailable", http.StatusBadGateway)
		}
		mounts = append(mounts, httpMount{prefix: prefix, proxy: reverseProxy})
	}
	registry.register(inst.Manifest.Descriptor.Identity.ID, mounted{process: proc, http: mounts})
	return nil
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
