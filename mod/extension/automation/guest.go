// Package automation exposes Gateway automation engines through the portable
// ext.plugin/v1 process contract. Extension binaries use this adapter without
// depending on Spinner's implementation packages.
package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
	driver "github.com/webong/gateway/driver/automation"
)

var automationContract = plugin.ContractRef{Name: "gateway.automation", Version: "v1"}

// Factory constructs one extension-owned automation engine.
type Factory func() (driver.Engine, error)

type packageManifest struct {
	Descriptor plugin.Descriptor `json:"descriptor"`
}

type executeRequest struct {
	Language  string         `json:"language"`
	Source    string         `json:"source"`
	Event     map[string]any `json:"event"`
	Variables map[string]any `json:"variables"`
}

type executeResponse struct {
	Outcome driver.Outcome `json:"outcome"`
}

// Serve starts an automation extension from the plugin.json in its working
// directory. The package descriptor, not the binary, owns extension identity.
func Serve(ctx context.Context, factory Factory) error {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		return fmt.Errorf("read automation extension manifest: %w", err)
	}
	var manifest packageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("decode automation extension manifest: %w", err)
	}
	guest, err := Guest(manifest.Descriptor, factory)
	if err != nil {
		return err
	}
	return jsonline.ServeStdio(ctx, guest)
}

// Guest adapts one engine to the portable automation operation.
func Guest(descriptor plugin.Descriptor, factory Factory) (*plugin.Guest, error) {
	if factory == nil {
		return nil, errors.New("automation engine factory is required")
	}
	engine, err := factory()
	if err != nil {
		return nil, fmt.Errorf("construct automation engine: %w", err)
	}
	language := string(engine.Language())
	if language == "" || !declaresOperation(descriptor.Contracts, "execute", language) {
		return nil, fmt.Errorf("extension descriptor does not declare automation language %q", language)
	}
	return plugin.NewGuest(descriptor, plugin.GuestOptions{Handler: func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
		var input executeRequest
		if err := json.Unmarshal(request.Payload, &input); err != nil {
			return nil, fmt.Errorf("decode automation request: %w", err)
		}
		if input.Language != "" && input.Language != language {
			return nil, fmt.Errorf("automation language %q does not match extension language %q", input.Language, language)
		}
		outcome, err := engine.Execute(ctx, input.Source, driver.Input{Event: input.Event, Variables: input.Variables})
		if err != nil {
			return nil, err
		}
		return json.Marshal(executeResponse{Outcome: outcome})
	}})
}

func declaresOperation(contracts []plugin.Contract, name, surface string) bool {
	for _, declared := range contracts {
		if declared.ContractRef != automationContract {
			continue
		}
		for _, operation := range declared.Operations {
			if operation.Name == name && operation.Surface == surface {
				return true
			}
		}
	}
	return false
}
