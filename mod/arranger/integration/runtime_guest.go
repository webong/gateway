package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
)

type packageManifest struct {
	Descriptor plugin.Descriptor `json:"descriptor"`
}

// RuntimeOperationHandler exposes additional contracts implemented by an
// Integration process alongside gateway.runtime/v1. The package descriptor is
// still the complete allowlist; this handler does not grant capabilities.
type RuntimeOperationHandler interface {
	HandleRuntimeOperation(context.Context, plugin.Request) (json.RawMessage, error)
}

type RuntimeOperationHandlerFunc func(context.Context, plugin.Request) (json.RawMessage, error)

func (f RuntimeOperationHandlerFunc) HandleRuntimeOperation(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
	return f(ctx, request)
}

// ServeRuntime starts a Spinner runtime entrypoint from the verified package in its
// working directory. stdout remains reserved for ext.plugin/v1 frames.
func ServeRuntime(ctx context.Context, starter RuntimeStarter) error {
	return ServeRuntimeWithHandler(ctx, starter, nil)
}

// ServeRuntimeWithHandler serves the lifecycle contract plus capability-owned
// operations from one verified Spinner entrypoint.
func ServeRuntimeWithHandler(ctx context.Context, starter RuntimeStarter, handler RuntimeOperationHandler) error {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		return fmt.Errorf("read runtime extension manifest: %w", err)
	}
	var manifest packageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("decode runtime extension manifest: %w", err)
	}
	guest, err := RuntimeGuestWithHandler(manifest.Descriptor, starter, handler)
	if err != nil {
		return err
	}
	return jsonline.ServeStdio(ctx, guest)
}

// RuntimeGuest adapts one process-owned runtime to the portable plugin contract.
func RuntimeGuest(descriptor plugin.Descriptor, starter RuntimeStarter) (*plugin.Guest, error) {
	return RuntimeGuestWithHandler(descriptor, starter, nil)
}

func RuntimeGuestWithHandler(descriptor plugin.Descriptor, starter RuntimeStarter, handler RuntimeOperationHandler) (*plugin.Guest, error) {
	if starter == nil {
		return nil, errors.New("runtime starter is required")
	}
	if _, err := descriptor.Lookup(RuntimeContract, OperationStart); err != nil {
		return nil, errors.New("extension descriptor does not declare the Spinner runtime contract")
	}
	var mu sync.Mutex
	started := false
	return plugin.NewGuest(descriptor, plugin.GuestOptions{Handler: func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
		if request.Contract != RuntimeContract || request.Operation != OperationStart {
			if handler != nil {
				return handler.HandleRuntimeOperation(ctx, request)
			}
			return nil, plugin.ErrUnsupported
		}
		mu.Lock()
		defer mu.Unlock()
		if started {
			return nil, errors.New("runtime is already started")
		}
		var input RuntimeStartRequest
		if err := plugin.Decode(request.Payload, &input); err != nil {
			return nil, fmt.Errorf("decode runtime start request: %w", err)
		}
		caller := plugin.SessionReverseFrom(ctx)
		if caller == nil {
			return nil, errors.New("runtime host services are unavailable")
		}
		result, err := starter.Start(ctx, input, caller)
		if err != nil {
			return nil, err
		}
		started = true
		return marshal(result)
	}})
}
