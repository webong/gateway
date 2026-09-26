package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HTTPProtocolPlanner sends protocol-neutral events to Laravel's internal
// event route. It is used by the Go listener and by the Caddy app
// module when PHP is reachable through an HTTP address.
type HTTPProtocolPlanner struct {
	Client        *http.Client
	EventURL      string
	InternalToken string
	MaxBodySize   int64
}

func NewHTTPProtocolPlanner(backendURL string, client *http.Client, maxBodySize int64, internalToken string) (*HTTPProtocolPlanner, error) {
	eventURL, err := internalEventURL(backendURL)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}

	return &HTTPProtocolPlanner{
		Client:        client,
		EventURL:      eventURL,
		InternalToken: internalToken,
		MaxBodySize:   maxBodySize,
	}, nil
}

func (p *HTTPProtocolPlanner) PlanEvent(ctx context.Context, event GatewayEvent) (GatewayDecision, error) {
	if p == nil || p.Client == nil {
		return GatewayDecision{}, fmt.Errorf("HTTP protocol planner client is not configured")
	}
	if strings.TrimSpace(p.EventURL) == "" {
		return GatewayDecision{}, fmt.Errorf("HTTP protocol planner URL is not configured")
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return GatewayDecision{}, fmt.Errorf("marshal gateway event: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.EventURL, bytes.NewReader(payload))
	if err != nil {
		return GatewayDecision{}, fmt.Errorf("create HTTP protocol planner request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	setInternalToken(request.Header, p.InternalToken)
	if event.Host != "" {
		request.Header.Set("X-Gateway-Original-Host", event.Host)
	}

	response, err := p.Client.Do(request)
	if err != nil {
		return GatewayDecision{}, fmt.Errorf("call HTTP protocol planner: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return GatewayDecision{}, fmt.Errorf("HTTP protocol planner returned status %d", response.StatusCode)
	}

	reader := io.Reader(response.Body)
	if p.MaxBodySize > 0 {
		reader = io.LimitReader(response.Body, p.MaxBodySize+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return GatewayDecision{}, fmt.Errorf("read gateway decision: %w", err)
	}
	if p.MaxBodySize > 0 && int64(len(body)) > p.MaxBodySize {
		return GatewayDecision{}, fmt.Errorf("gateway decision exceeds maximum size of %d bytes", p.MaxBodySize)
	}

	var decision GatewayDecision
	if err := json.Unmarshal(body, &decision); err != nil {
		return GatewayDecision{}, fmt.Errorf("decode gateway decision: %w", err)
	}
	if err := decision.Validate(); err != nil {
		return GatewayDecision{}, err
	}

	return decision, nil
}

func internalEventURL(backendURL string) (string, error) {
	backend, err := parseBackendURL(backendURL)
	if err != nil {
		return "", err
	}

	eventPath := strings.TrimSuffix(backend.Path, "/") + "/_internal/gateway/event"
	backend.Path = eventPath
	backend.RawQuery = ""
	backend.Fragment = ""

	return backend.String(), nil
}

var _ ProtocolPlanner = (*HTTPProtocolPlanner)(nil)
