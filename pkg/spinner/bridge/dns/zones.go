package dns

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"
)

// ZoneSource is the sole input seam for authoritative-zone snapshots.
type ZoneSource interface {
	Zones(context.Context) ([]Zone, error)
}

type HTTPZoneSource struct {
	url    string
	token  string
	client *http.Client
}

type HandlerZoneSource struct {
	handler http.Handler
	token   string
}

func NewHandlerZoneSource(handler http.Handler, token string) (*HandlerZoneSource, error) {
	if handler == nil {
		return nil, fmt.Errorf("DNS zone source requires a PHP handler")
	}
	if token == "" {
		return nil, fmt.Errorf("DNS zone source requires an internal token")
	}
	return &HandlerZoneSource{handler: handler, token: token}, nil
}

func NewHTTPZoneSource(backendURL, token string, client *http.Client) (*HTTPZoneSource, error) {
	endpoint, err := url.JoinPath(backendURL, "_internal/gateway/dns/zones")
	if err != nil {
		return nil, fmt.Errorf("invalid DNS zone source URL: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("DNS zone source requires an internal token")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPZoneSource{url: endpoint, token: token, client: client}, nil
}

func (s *HTTPZoneSource) Zones(ctx context.Context) ([]Zone, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("create DNS zone snapshot request: %w", err)
	}
	request.Header.Set("X-Gateway-Internal", s.token)
	response, err := s.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch DNS zone snapshot: %w", err)
	}
	defer response.Body.Close()
	return decodeZoneSnapshot(response)
}

func (s *HandlerZoneSource) Zones(ctx context.Context) ([]Zone, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://gateway.internal/_internal/gateway/dns/zones", nil)
	if err != nil {
		return nil, fmt.Errorf("create DNS zone snapshot request: %w", err)
	}
	request.Header.Set("X-Gateway-Internal", s.token)
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	return decodeZoneSnapshot(response)
}

func decodeZoneSnapshot(response *http.Response) ([]Zone, error) {
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch DNS zone snapshot: unexpected HTTP status %d", response.StatusCode)
	}
	var document struct {
		Data []Zone `json:"data"`
	}
	decoder := json.NewDecoder(response.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode DNS zone snapshot: %w", err)
	}
	return document.Data, nil
}

type ZoneReloader struct {
	Server   *Server
	Source   ZoneSource
	Interval time.Duration
	OnError  func(string, ...any)
}

func (r ZoneReloader) Refresh(ctx context.Context) error {
	if r.Server == nil || r.Source == nil {
		return fmt.Errorf("DNS zone reloader requires a server and source")
	}
	zones, err := r.Source.Zones(ctx)
	if err != nil {
		return err
	}
	return r.Server.ReplaceZones(zones)
}

func (r ZoneReloader) Start(ctx context.Context) {
	if r.Interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(r.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.Refresh(ctx); err != nil && r.OnError != nil {
					r.OnError("Failed to refresh authoritative DNS zones: %v", err)
				}
			}
		}
	}()
}
