// Package companionextensions exposes Planner companion actions to portable
// Spinner plugins through the ext protocol's declared reverse-call interface.
// Plugins receive no Planner URL or internal credential and may invoke only
// action names declared in their reviewed descriptor.
package companionextensions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/driver/companion"
)

var Contract = plugin.ContractRef{Name: "gateway.companion", Version: companion.ProtocolVersion}

// Host is a late-bound adapter from portable plugin reverse calls to the
// transport-neutral Planner runner. Plugins may be loaded before Planner is
// ready; calls fail closed until SetRunner installs the live adapter.
type Host struct {
	mu     sync.RWMutex
	runner companion.Runner
}

func NewHost() *Host { return &Host{} }

var DefaultHost = NewHost()

func Configure(runner companion.Runner) { DefaultHost.SetRunner(runner) }

func (h *Host) SetRunner(runner companion.Runner) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runner = runner
}

func (h *Host) current() companion.Runner {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.runner
}

// ReverseOptions returns host policy for a plugin session. The plugin's
// descriptor must declare gateway.companion/v1 and each exact action name as
// an operation; a generic invoke permission is intentionally unsupported.
func (h *Host) ReverseOptions() *plugin.ReverseOptions {
	return &plugin.ReverseOptions{
		Handle:          h.handle,
		Authorize:       h.authorize,
		Timeout:         5 * time.Minute,
		MaxPayloadBytes: 1 << 20,
		MaxInFlight:     4,
	}
}

func (h *Host) authorize(_ context.Context, request plugin.Request) error {
	if request.Contract != Contract {
		return plugin.ErrDenied
	}
	invocation, err := decodeInvocation(request.Payload, request.Deadline)
	if err != nil || invocation.Action != request.Operation {
		return plugin.ErrDenied
	}
	return nil
}

func (h *Host) handle(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
	invocation, err := decodeInvocation(request.Payload, request.Deadline)
	if err != nil {
		return nil, &plugin.RemoteError{Code: string(companion.ErrorInvalid), Message: "invalid companion action invocation"}
	}
	runner := h.current()
	if runner == nil {
		return nil, &plugin.RemoteError{Code: string(companion.ErrorUnavailable), Message: "companion action runner is unavailable"}
	}
	result, err := runner.Invoke(ctx, invocation)
	if err != nil {
		var runnerError *companion.RunnerError
		if errors.As(err, &runnerError) {
			return nil, &plugin.RemoteError{Code: string(runnerError.Code), Message: runnerError.Error()}
		}
		return nil, &plugin.RemoteError{Code: string(companion.ErrorUnavailable), Message: "companion action runner is unavailable"}
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, &plugin.RemoteError{Code: string(companion.ErrorUnavailable), Message: "companion action result is unavailable"}
	}
	return payload, nil
}

func decodeInvocation(payload json.RawMessage, fallbackDeadline time.Time) (companion.Invocation, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var invocation companion.Invocation
	if err := decoder.Decode(&invocation); err != nil {
		return companion.Invocation{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return companion.Invocation{}, errors.New("companion invocation contains trailing data")
	}
	if invocation.Deadline == nil && !fallbackDeadline.IsZero() {
		fallbackDeadline = fallbackDeadline.UTC()
		invocation.Deadline = &fallbackDeadline
	}
	if err := invocation.Validate(); err != nil {
		return companion.Invocation{}, err
	}
	return invocation, nil
}
