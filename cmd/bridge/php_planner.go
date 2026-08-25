package webrelay

// PHPPlanner invokes the Laravel worker through the RoadRunner chain.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
)

const PHPPlannerPath = "/_internal/web-relay/plan"

// PHPPlanner invokes the Laravel worker through the next handler in the
// RoadRunner HTTP middleware chain. The internal path is never exposed as a
// public route.
type PHPPlanner struct {
	PHPHandler    http.Handler
	PlanPath      string
	InternalToken string
	MaxBodySize   int64
}

func NewPHPPlanner(handler http.Handler, maxBodySize int64) *PHPPlanner {
	return &PHPPlanner{
		PHPHandler:  handler,
		PlanPath:    PHPPlannerPath,
		MaxBodySize: maxBodySize,
	}
}

func (p *PHPPlanner) Plan(ctx context.Context, ingress IngressRequest) (RoutePlan, error) {
	if p.PHPHandler == nil {
		return RoutePlan{}, fmt.Errorf("PHP planner handler is not configured")
	}

	payload, err := json.Marshal(ingress)
	if err != nil {
		return RoutePlan{}, fmt.Errorf("marshal ingress request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://roadrunner.internal"+p.PlanPath, bytes.NewReader(payload))
	if err != nil {
		return RoutePlan{}, fmt.Errorf("create PHP planner request: %w", err)
	}
	request.Host = ingress.Host
	request.Header.Set("Content-Type", "application/json")
	setInternalToken(request.Header, p.InternalToken)

	response := httptest.NewRecorder()
	p.PHPHandler.ServeHTTP(response, request)
	if response.Code < http.StatusOK || response.Code >= http.StatusMultipleChoices {
		return RoutePlan{}, fmt.Errorf("PHP planner returned status %d", response.Code)
	}

	body := response.Body
	if p.MaxBodySize > 0 && int64(body.Len()) > p.MaxBodySize {
		return RoutePlan{}, fmt.Errorf("PHP planner response exceeds maximum size of %d bytes", p.MaxBodySize)
	}

	var plan RoutePlan
	var reader io.Reader = body
	if p.MaxBodySize > 0 {
		reader = io.LimitReader(body, p.MaxBodySize+1)
	}
	if err := json.NewDecoder(reader).Decode(&plan); err != nil {
		return RoutePlan{}, fmt.Errorf("decode PHP route plan: %w", err)
	}
	if err := plan.Validate(); err != nil {
		return RoutePlan{}, err
	}

	return plan, nil
}

var _ Planner = (*PHPPlanner)(nil)
