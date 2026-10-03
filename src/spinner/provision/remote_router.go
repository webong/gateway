package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// RemoteRouter resolves managed routes from Planner for a public Spinner that
// does not run its own provisioning worker. A lookup is made for each request
// so stopped or expired instances are never served from a local route cache.
type RemoteRouter struct {
	base      *url.URL
	token     string
	client    *http.Client
	transport *http.Transport
	allowed   []netip.Prefix
}

func NewRemoteRouter(backendURL, token string, client *http.Client, allowedCIDRs []string) (*RemoteRouter, error) {
	base, err := url.Parse(strings.TrimSpace(backendURL))
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("managed route Planner URL must be an HTTP(S) origin")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("managed route lookup requires GATEWAY_INTERNAL_TOKEN")
	}
	if len(allowedCIDRs) == 0 {
		return nil, fmt.Errorf("managed route proxy requires GATEWAY_PROVISIONING_ROUTE_CIDRS")
	}
	allowed := make([]netip.Prefix, 0, len(allowedCIDRs))
	for _, cidr := range allowedCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return nil, fmt.Errorf("invalid managed route CIDR %q: %w", cidr, err)
		}
		allowed = append(allowed, prefix.Masked())
	}
	if client == nil {
		client = http.DefaultClient
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &RemoteRouter{base: base, token: token, client: client, transport: transport, allowed: allowed}, nil
}

func (r *RemoteRouter) ServeIfMatched(writer http.ResponseWriter, request *http.Request) bool {
	return r.serveIfMatched(writer, request, r.client)
}

// ServeWithPlanner adapts an embedded PHP handler to the same authenticated
// lookup contract without opening a second Planner listener.
func (r *RemoteRouter) ServeWithPlanner(writer http.ResponseWriter, request *http.Request, planner http.Handler) bool {
	client := &http.Client{Transport: plannerRoundTripper{handler: planner}}
	return r.serveIfMatched(writer, request, client)
}

type plannerRoundTripper struct{ handler http.Handler }

func (p plannerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	// RoadRunner's PHP HTTP handler wraps and reads every request body,
	// including GET probes. A nil Body panics inside its size-limit wrapper.
	if request.Body == nil {
		request = request.Clone(request.Context())
		request.Body = http.NoBody
	}
	p.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func (r *RemoteRouter) serveIfMatched(writer http.ResponseWriter, request *http.Request, client *http.Client) bool {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	lookup := *r.base
	lookup.Path = strings.TrimRight(r.base.Path, "/") + "/_internal/provisioning/routes/resolve"
	query := lookup.Query()
	hostname := request.Host
	if host, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = host
	}
	query.Set("hostname", hostname)
	query.Set("path", request.URL.Path)
	lookup.RawQuery = query.Encode()
	probe, err := http.NewRequestWithContext(ctx, http.MethodGet, lookup.String(), nil)
	if err != nil {
		http.Error(writer, "Managed route lookup failed", http.StatusBadGateway)
		return true
	}
	probe.Header.Set("X-Gateway-Internal", r.token)
	lookupClient := *client
	lookupClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := lookupClient.Do(probe)
	if err != nil {
		http.Error(writer, "Managed route lookup failed", http.StatusBadGateway)
		return true
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return false
	}
	if response.StatusCode == http.StatusServiceUnavailable {
		http.Error(writer, "Managed server has no healthy instance", http.StatusServiceUnavailable)
		return true
	}
	if response.StatusCode != http.StatusOK {
		http.Error(writer, "Managed route lookup failed", http.StatusBadGateway)
		return true
	}
	var document struct {
		Data struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4096))
	if err := decoder.Decode(&document); err != nil || document.Data.Port < 1 || document.Data.Port > 65535 {
		http.Error(writer, "Managed route lookup returned an invalid instance", http.StatusBadGateway)
		return true
	}
	address, addressErr := netip.ParseAddr(document.Data.Host)
	if addressErr != nil {
		http.Error(writer, "Managed route lookup returned an invalid instance", http.StatusBadGateway)
		return true
	}
	permitted := false
	for _, prefix := range r.allowed {
		if prefix.Contains(address.Unmap()) {
			permitted = true
			break
		}
	}
	if !permitted {
		http.Error(writer, "Managed route instance is outside allowed networks", http.StatusBadGateway)
		return true
	}
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort(document.Data.Host, strconv.Itoa(document.Data.Port))}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = r.transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "Provisioned instance unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(writer, request)
	return true
}
