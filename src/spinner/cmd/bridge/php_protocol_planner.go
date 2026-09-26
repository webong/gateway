package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
)

const PHPProtocolEventPath = "/_internal/gateway/event"

// PHPProtocolPlanner invokes the Laravel protocol planner through an
// in-process HTTP handler. Embedded RoadRunner and FrankenPHP/Caddy use this
// shape; the HTTP runtime uses HTTPProtocolPlanner for a remote backend.
type PHPProtocolPlanner struct {
	PHPHandler    http.Handler
	EventPath     string
	InternalToken string
	MaxBodySize   int64
}

func NewPHPProtocolPlanner(handler http.Handler, maxBodySize int64) *PHPProtocolPlanner {
	return &PHPProtocolPlanner{
		PHPHandler:  handler,
		EventPath:   PHPProtocolEventPath,
		MaxBodySize: maxBodySize,
	}
}

func (p *PHPProtocolPlanner) PlanEvent(ctx context.Context, event GatewayEvent) (GatewayDecision, error) {
	if p == nil || p.PHPHandler == nil {
		return GatewayDecision{}, fmt.Errorf("PHP protocol planner handler is not configured")
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return GatewayDecision{}, fmt.Errorf("marshal gateway event: %w", err)
	}
	eventPath := p.EventPath
	if eventPath == "" {
		eventPath = PHPProtocolEventPath
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://gateway.internal"+eventPath, bytes.NewReader(payload))
	if err != nil {
		return GatewayDecision{}, fmt.Errorf("create PHP protocol planner request: %w", err)
	}
	request.Host = event.Host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	setInternalToken(request.Header, p.InternalToken)

	response := httptest.NewRecorder()
	p.PHPHandler.ServeHTTP(response, request)
	if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
		return GatewayDecision{}, fmt.Errorf("PHP protocol planner returned status %d", response.Code)
	}
	if p.MaxBodySize > 0 && int64(response.Body.Len()) > p.MaxBodySize {
		return GatewayDecision{}, fmt.Errorf("PHP gateway decision exceeds maximum size of %d bytes", p.MaxBodySize)
	}

	var decision GatewayDecision
	if err := json.Unmarshal(response.Body.Bytes(), &decision); err != nil {
		return GatewayDecision{}, fmt.Errorf("decode PHP gateway decision: %w", err)
	}
	if err := decision.Validate(); err != nil {
		return GatewayDecision{}, err
	}

	return decision, nil
}

var _ ProtocolPlanner = (*PHPProtocolPlanner)(nil)
