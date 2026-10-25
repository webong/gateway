package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/webong/gateway/arranger/integration"
)

const ObservationPath = "/_internal/gateway/observations"

// PHPObservationSink sends language-neutral observations through an embedded
// Planner handler. It is called by a bounded integration queue, never by the
// answer-producing path directly.
type PHPObservationSink struct {
	PHPHandler    http.Handler
	InternalToken string
}

func NewPHPObservationSink(handler http.Handler, internalToken string) *PHPObservationSink {
	return &PHPObservationSink{PHPHandler: handler, InternalToken: internalToken}
}

func (s *PHPObservationSink) RecordObservation(ctx context.Context, observation integration.Observation) error {
	if s == nil || s.PHPHandler == nil {
		return fmt.Errorf("PHP observation handler is not configured")
	}
	request, err := observationRequest(ctx, "http://gateway.internal"+ObservationPath, s.InternalToken, observation)
	if err != nil {
		return err
	}
	recorder := httptest.NewRecorder()
	s.PHPHandler.ServeHTTP(recorder, request)
	if recorder.Code < http.StatusOK || recorder.Code >= http.StatusMultipleChoices {
		return fmt.Errorf("PHP observation handler returned status %d", recorder.Code)
	}
	return nil
}

type HTTPObservationSink struct {
	Client        *http.Client
	URL           string
	InternalToken string
}

func NewHTTPObservationSink(plannerURL string, client *http.Client, internalToken string) (*HTTPObservationSink, error) {
	backend, err := parsePlannerURL(plannerURL)
	if err != nil {
		return nil, err
	}
	backend.Path = strings.TrimSuffix(backend.Path, "/") + ObservationPath
	backend.RawPath = ""
	backend.RawQuery = ""
	backend.Fragment = ""
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPObservationSink{Client: client, URL: backend.String(), InternalToken: internalToken}, nil
}

func (s *HTTPObservationSink) RecordObservation(ctx context.Context, observation integration.Observation) error {
	if s == nil || s.Client == nil || s.URL == "" {
		return fmt.Errorf("HTTP observation sink is not configured")
	}
	request, err := observationRequest(ctx, s.URL, s.InternalToken, observation)
	if err != nil {
		return err
	}
	response, err := s.Client.Do(request)
	if err != nil {
		return fmt.Errorf("call Planner observation sink: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Planner observation sink returned status %d", response.StatusCode)
	}
	return nil
}

func observationRequest(ctx context.Context, target, internalToken string, observation integration.Observation) (*http.Request, error) {
	if err := observation.Validate(); err != nil {
		return nil, err
	}
	if _, err := url.ParseRequestURI(target); err != nil {
		return nil, fmt.Errorf("invalid observation URL: %w", err)
	}
	payload, err := json.Marshal(observation)
	if err != nil {
		return nil, fmt.Errorf("marshal observation: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create observation request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	setInternalToken(request.Header, internalToken)
	return request, nil
}

var _ integration.ObservationSink = (*PHPObservationSink)(nil)
var _ integration.ObservationSink = (*HTTPObservationSink)(nil)
