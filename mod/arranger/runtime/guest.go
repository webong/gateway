package runtime

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

// Serve starts a Spinner runtime entrypoint from the verified package in its
// working directory. stdout remains reserved for ext.plugin/v1 frames.
func Serve(ctx context.Context, starter Starter) error {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		return fmt.Errorf("read runtime extension manifest: %w", err)
	}
	var manifest packageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("decode runtime extension manifest: %w", err)
	}
	guest, err := Guest(manifest.Descriptor, starter)
	if err != nil {
		return err
	}
	return jsonline.ServeStdio(ctx, guest)
}

// Guest adapts one process-owned runtime to the portable plugin contract.
func Guest(descriptor plugin.Descriptor, starter Starter) (*plugin.Guest, error) {
	if starter == nil {
		return nil, errors.New("runtime starter is required")
	}
	if _, err := descriptor.Lookup(Contract, OperationStart); err != nil {
		return nil, errors.New("extension descriptor does not declare the Spinner runtime contract")
	}
	var mu sync.Mutex
	started := false
	return plugin.NewGuest(descriptor, plugin.GuestOptions{Handler: func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
		if request.Contract != Contract || request.Operation != OperationStart {
			return nil, plugin.ErrUnsupported
		}
		mu.Lock()
		defer mu.Unlock()
		if started {
			return nil, errors.New("runtime is already started")
		}
		var input StartRequest
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
