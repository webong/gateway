package gateway

// HTTPRuntime connects the Go edge to an independently hosted Laravel
// application. It uses ordinary HTTP for the PHP control-plane call and for
// pass-through requests, while keeping the same Planner contract used by the
// embedded RoadRunner adapter.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

const (
	InternalTokenHeader = "X-Gateway-Internal"
)

// HTTPPlanner asks the Laravel application to produce a route plan over an
// ordinary HTTP request. Laravel remains the owner of validation, registry
// lookup, subscriber resolution, and path binding; this adapter only carries
// the versioned bridge document.
type HTTPPlanner struct {
	Client        *http.Client
	PlanURL       string
	InternalToken string
	MaxBodySize   int64
}

func NewHTTPPlanner(backendURL string, client *http.Client, maxBodySize int64, internalToken string) (*HTTPPlanner, error) {
	planURL, err := internalPlanURL(backendURL)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}

	return &HTTPPlanner{
		Client:        client,
		PlanURL:       planURL,
		InternalToken: internalToken,
		MaxBodySize:   maxBodySize,
	}, nil
}

func (p *HTTPPlanner) Plan(ctx context.Context, ingress IngressRequest) (RoutePlan, error) {
	if p == nil || p.Client == nil {
		return RoutePlan{}, fmt.Errorf("HTTP planner client is not configured")
	}
	if strings.TrimSpace(p.PlanURL) == "" {
		return RoutePlan{}, fmt.Errorf("HTTP planner URL is not configured")
	}

	payload, err := json.Marshal(ingress)
	if err != nil {
		return RoutePlan{}, fmt.Errorf("marshal ingress request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.PlanURL, bytes.NewReader(payload))
	if err != nil {
		return RoutePlan{}, fmt.Errorf("create HTTP planner request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	setInternalToken(request.Header, p.InternalToken)
	if ingress.Host != "" {
		request.Header.Set("X-Gateway-Original-Host", ingress.Host)
	}

	response, err := p.Client.Do(request)
	if err != nil {
		return RoutePlan{}, fmt.Errorf("call HTTP planner: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return RoutePlan{}, fmt.Errorf("HTTP planner returned status %d", response.StatusCode)
	}

	reader := io.Reader(response.Body)
	if p.MaxBodySize > 0 {
		reader = io.LimitReader(response.Body, p.MaxBodySize+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return RoutePlan{}, fmt.Errorf("read HTTP route plan: %w", err)
	}
	if p.MaxBodySize > 0 && int64(len(body)) > p.MaxBodySize {
		return RoutePlan{}, fmt.Errorf("HTTP planner response exceeds maximum size of %d bytes", p.MaxBodySize)
	}

	var plan RoutePlan
	if err := json.Unmarshal(body, &plan); err != nil {
		return RoutePlan{}, fmt.Errorf("decode HTTP route plan: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return RoutePlan{}, err
	}

	return plan, nil
}

// HTTPRuntime supplies the two application-facing adapters needed by Edge:
// a planner endpoint and a pass-through reverse proxy.
type HTTPRuntime struct {
	planner     *HTTPPlanner
	passThrough *httputil.ReverseProxy
	transport   http.RoundTripper
	planPath    string
}

func NewHTTPRuntime(backendURL string, internalToken string, maxBodySize int64, client *http.Client) (*HTTPRuntime, error) {
	backend, err := parseBackendURL(backendURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(internalToken) == "" {
		return nil, fmt.Errorf("GATEWAY_INTERNAL_TOKEN is required for HTTP runtime")
	}
	if client == nil {
		client = http.DefaultClient
	}

	planner, err := NewHTTPPlanner(backend.String(), client, maxBodySize, internalToken)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(backend)
	proxy.Transport = client.Transport
	proxy.ErrorHandler = func(writer http.ResponseWriter, _ *http.Request, err error) {
		http.Error(writer, "Laravel backend unavailable", http.StatusBadGateway)
	}

	return &HTTPRuntime{
		planner:     planner,
		passThrough: proxy,
		transport:   client.Transport,
		planPath:    PHPPlannerPath,
	}, nil
}

func (r *HTTPRuntime) Planner() Planner {
	if r == nil {
		return nil
	}
	return r.planner
}

func (r *HTTPRuntime) PassThrough() http.Handler {
	if r == nil {
		return nil
	}
	return r.passThrough
}

// Handler protects the internal planner path at the Go public edge. The
// Laravel backend still serves that route privately for the HTTP planner.
func (r *HTTPRuntime) Handler(edge http.Handler) http.Handler {
	planPath := PHPPlannerPath
	if r != nil && r.planPath != "" {
		planPath = r.planPath
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if isPathUnder(request.URL.Path, planPath) {
			http.NotFound(writer, request)
			return
		}
		edge.ServeHTTP(writer, request)
	})
}

// Close releases idle backend connections when the host shuts down. The
// default transport is intentionally left alone because it may be shared by
// other clients in the process.
func (r *HTTPRuntime) Close() {
	if r == nil || r.transport == nil {
		return
	}
	if closer, ok := r.transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func parseBackendURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("GATEWAY_LARAVEL_BACKEND_URL is required for HTTP runtime")
	}

	backend, err := url.Parse(raw)
	if err != nil || backend.Scheme == "" || backend.Host == "" {
		return nil, fmt.Errorf("invalid GATEWAY_LARAVEL_BACKEND_URL %q", raw)
	}
	if backend.Scheme != "http" && backend.Scheme != "https" {
		return nil, fmt.Errorf("GATEWAY_LARAVEL_BACKEND_URL must use http or https")
	}

	return backend, nil
}

func internalPlanURL(backendURL string) (string, error) {
	backend, err := parseBackendURL(backendURL)
	if err != nil {
		return "", err
	}

	planPath := strings.TrimSuffix(backend.Path, "/") + PHPPlannerPath
	backend.Path = planPath
	backend.RawQuery = ""
	backend.Fragment = ""

	return backend.String(), nil
}

func setInternalToken(headers http.Header, token string) {
	if token == "" {
		token = "1"
	}
	headers.Set(InternalTokenHeader, token)
}

var _ Planner = (*HTTPPlanner)(nil)
