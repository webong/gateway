// Package secret provides identity-scoped Planner secret lookup to installed
// Spinner guests without coupling automation guests to integration hosting.
package secret

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/integration"
)

type Host struct {
	mu          sync.RWMutex
	passThrough http.Handler
	token       string
}

func NewHost() *Host { return &Host{} }

var DefaultHost = NewHost()

func (h *Host) Configure(passThrough http.Handler, token string) {
	h.mu.Lock()
	h.passThrough, h.token = passThrough, token
	h.mu.Unlock()
}

func (h *Host) Authorize(identity plugin.Identity, request plugin.Request) error {
	if request.Contract != integration.SecretContract || request.Operation != integration.OperationResolveSecret ||
		identity.ID == "" || identity.Validate() != nil {
		return plugin.ErrDenied
	}
	h.mu.RLock()
	ready := h.passThrough != nil && h.token != ""
	h.mu.RUnlock()
	if !ready {
		return plugin.ErrDenied
	}
	return nil
}

func (h *Host) Handle(ctx context.Context, identity plugin.Identity, request plugin.Request) (json.RawMessage, error) {
	if err := h.Authorize(identity, request); err != nil {
		return nil, err
	}
	var input integration.SecretRequest
	if err := decode(request.Payload, &input); err != nil || !integration.ValidSecretReference(input.Reference) {
		return nil, plugin.ErrInvalid
	}
	h.mu.RLock()
	passThrough, token := h.passThrough, h.token
	h.mu.RUnlock()
	if passThrough == nil || token == "" {
		return nil, plugin.ErrDenied
	}
	path := "/_internal/gateway/extensions/" + identity.ID + "/secrets/" + input.Reference
	plannerRequest := httptest.NewRequest(http.MethodGet, "http://gateway.internal"+path, nil).WithContext(ctx)
	plannerRequest.Header.Set("X-Gateway-Internal", token)
	recorder := httptest.NewRecorder()
	passThrough.ServeHTTP(recorder, plannerRequest)
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, plugin.ErrDenied
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return nil, plugin.ErrInvalid
	}
	var value integration.SecretValue
	if err := decode(body, &value); err != nil || value.Reference != input.Reference || value.Version < 1 || len(value.Value) == 0 {
		return nil, plugin.ErrInvalid
	}
	return json.Marshal(value)
}

func decode(payload []byte, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return plugin.ErrInvalid
	}
	return nil
}

// ReverseOptionsFor grants only secret resolution, then delegates other
// operations to the supplied host (for example trusted companion actions).
func (h *Host) ReverseOptionsFor(identity plugin.Identity, fallback *plugin.ReverseOptions) *plugin.ReverseOptions {
	return &plugin.ReverseOptions{
		Authorize: func(ctx context.Context, request plugin.Request) error {
			if request.Contract == integration.SecretContract {
				return h.Authorize(identity, request)
			}
			if fallback == nil || fallback.Authorize == nil {
				return plugin.ErrDenied
			}
			return fallback.Authorize(ctx, request)
		},
		Handle: func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
			if request.Contract == integration.SecretContract {
				return h.Handle(ctx, identity, request)
			}
			if fallback == nil || fallback.Handle == nil {
				return nil, plugin.ErrDenied
			}
			return fallback.Handle(ctx, request)
		},
		Timeout: 5 * time.Minute, MaxPayloadBytes: plugin.MaxFrameBytes - (64 << 10), MaxInFlight: 16,
	}
}
