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

	"github.com/webong/gateway/driver/companion"
)

const companionActionsPath = "/_internal/gateway/companion-actions"

type companionRequester interface {
	Do(*http.Request) (*http.Response, error)
}

type companionRunner struct {
	requester     companionRequester
	baseURL       string
	internalToken string
	maxBodySize   int64
}

// HTTPCompanionRunner invokes trusted PHP companion actions in an externally
// hosted Planner over its private HTTP listener.
type HTTPCompanionRunner struct{ *companionRunner }

func NewHTTPCompanionRunner(plannerURL string, client *http.Client, maxBodySize int64, internalToken string) (*HTTPCompanionRunner, error) {
	backend, err := parsePlannerURL(plannerURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(internalToken) == "" {
		return nil, fmt.Errorf("GATEWAY_INTERNAL_TOKEN is required for companion actions")
	}
	if client == nil {
		client = http.DefaultClient
	}
	backend.Path = strings.TrimRight(backend.Path, "/") + companionActionsPath
	backend.RawQuery = ""
	backend.Fragment = ""

	return &HTTPCompanionRunner{&companionRunner{
		requester: client, baseURL: backend.String(), internalToken: internalToken, maxBodySize: maxBodySize,
	}}, nil
}

// PHPCompanionRunner invokes the same interface against an in-process Planner
// handler used by bundled Router deployments.
type PHPCompanionRunner struct{ *companionRunner }

func NewPHPCompanionRunner(handler http.Handler, maxBodySize int64, internalToken string) (*PHPCompanionRunner, error) {
	if handler == nil {
		return nil, fmt.Errorf("PHP companion action handler is not configured")
	}
	if strings.TrimSpace(internalToken) == "" {
		return nil, fmt.Errorf("GATEWAY_INTERNAL_TOKEN is required for companion actions")
	}
	return &PHPCompanionRunner{&companionRunner{
		requester:     inProcessRequester{handler: handler},
		baseURL:       "http://planner.internal" + companionActionsPath,
		internalToken: internalToken,
		maxBodySize:   maxBodySize,
	}}, nil
}

func (r *companionRunner) Capabilities(ctx context.Context) ([]companion.Capability, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create companion capability request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	setInternalToken(request.Header, r.internalToken)

	response, err := r.requester.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query companion capabilities: %w", err)
	}
	defer response.Body.Close()
	if err := requireCompanionSuccess(response, r.maxBodySize); err != nil {
		return nil, err
	}

	var document struct {
		ProtocolVersion string                 `json:"protocol_version"`
		Actions         []companion.Capability `json:"actions"`
	}
	if err := decodeCompanionJSON(response.Body, r.maxBodySize, &document); err != nil {
		return nil, fmt.Errorf("decode companion capabilities: %w", err)
	}
	if document.ProtocolVersion != companion.ProtocolVersion {
		return nil, fmt.Errorf("unsupported companion protocol version %q", document.ProtocolVersion)
	}
	for _, capability := range document.Actions {
		if err := capability.Validate(); err != nil {
			return nil, fmt.Errorf("invalid companion capability: %w", err)
		}
	}
	return document.Actions, nil
}

func (r *companionRunner) Invoke(ctx context.Context, invocation companion.Invocation) (companion.Result, error) {
	if err := invocation.Validate(); err != nil {
		return companion.Result{}, err
	}
	payload, err := json.Marshal(invocation)
	if err != nil {
		return companion.Result{}, fmt.Errorf("marshal companion invocation: %w", err)
	}
	action := url.PathEscape(invocation.Action)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/"+action+"/executions", bytes.NewReader(payload))
	if err != nil {
		return companion.Result{}, fmt.Errorf("create companion invocation request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	setInternalToken(request.Header, r.internalToken)

	response, err := r.requester.Do(request)
	if err != nil {
		return companion.Result{}, fmt.Errorf("invoke companion action: %w", err)
	}
	defer response.Body.Close()
	if err := requireCompanionSuccess(response, r.maxBodySize); err != nil {
		return companion.Result{}, err
	}

	var result companion.Result
	if err := decodeCompanionJSON(response.Body, r.maxBodySize, &result); err != nil {
		return companion.Result{}, fmt.Errorf("decode companion action result: %w", err)
	}
	if err := result.Validate(); err != nil {
		return companion.Result{}, err
	}
	return result, nil
}

func requireCompanionSuccess(response *http.Response, maxBodySize int64) error {
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return nil
	}
	body, _ := readCompanionBody(response.Body, maxBodySize)
	var problem struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &problem)
	message := strings.TrimSpace(problem.Message)
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return fmt.Errorf("companion action runner returned status %d: %s", response.StatusCode, message)
}

func decodeCompanionJSON(reader io.Reader, maxBodySize int64, target any) error {
	body, err := readCompanionBody(reader, maxBodySize)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return err
	}
	return nil
}

func readCompanionBody(reader io.Reader, maxBodySize int64) ([]byte, error) {
	if maxBodySize <= 0 {
		return io.ReadAll(reader)
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBodySize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBodySize {
		return nil, fmt.Errorf("companion response exceeds maximum size of %d bytes", maxBodySize)
	}
	return body, nil
}

type inProcessRequester struct{ handler http.Handler }

func (r inProcessRequester) Do(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	r.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

var _ companion.Runner = (*HTTPCompanionRunner)(nil)
var _ companion.Runner = (*PHPCompanionRunner)(nil)
