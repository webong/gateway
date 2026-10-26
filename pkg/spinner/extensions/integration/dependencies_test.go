package integrationextensions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
)

type dependencyProcess struct {
	descriptor plugin.Descriptor
	called     plugin.Request
	response   json.RawMessage
	err        error
}

func (p *dependencyProcess) Descriptor() plugin.Descriptor { return p.descriptor }

func (p *dependencyProcess) Call(_ context.Context, method string, params any, result any) error {
	ref, operation, err := p.descriptor.ResolveMethod(method)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	p.called = plugin.Request{Contract: ref, Operation: operation, Payload: payload}
	if p.err != nil {
		return p.err
	}
	if result != nil && len(p.response) > 0 {
		if output, ok := result.(*json.RawMessage); ok {
			*output = append((*output)[:0], p.response...)
		} else if err := json.Unmarshal(p.response, result); err != nil {
			return err
		}
	}
	return nil
}

func (*dependencyProcess) Close() error { return nil }

func TestDependencyReverseOptionsRouteOnlyTheResolvedProvider(t *testing.T) {
	contract := plugin.ContractRef{Name: "gateway.dns", Version: "v1"}
	providerID := plugin.Identity{ID: "webong/dns", Revision: "dns-r1", Version: "1.0.0"}
	consumerID := plugin.Identity{ID: "webong/email", Revision: "email-r1", Version: "1.0.0"}
	provider := &dependencyProcess{
		descriptor: plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: providerID, Contracts: []plugin.Contract{{
			ContractRef: contract, Operations: []plugin.Operation{{Name: "exchange"}},
		}}},
		response: json.RawMessage(`{"message":"response"}`),
	}
	registry := NewRegistry()
	registry.register(providerID.ID, mounted{identity: providerID, process: provider})
	bindings := []packagekit.Resolution{{
		Consumer: consumerID, Provider: providerID,
		Requirement: packagekit.Dependency{Contract: contract, Operation: "exchange"},
	}}
	reverse := registry.DependencyReverseOptions(consumerID, bindings, nil)
	request := plugin.Request{Contract: contract, Operation: "exchange", Payload: json.RawMessage(`{"message":"query"}`)}
	if err := reverse.Authorize(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	response, err := reverse.Handle(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != `{"message":"response"}` || provider.called.Contract != contract || provider.called.Operation != "exchange" {
		t.Fatalf("dependency call was not routed to the resolved provider: response=%s request=%+v", response, provider.called)
	}

	if err := reverse.Authorize(t.Context(), plugin.Request{Contract: contract, Operation: "other"}); !errors.Is(err, plugin.ErrDenied) {
		t.Fatalf("undeclared dependency operation was not denied: %v", err)
	}
}

func TestDependencyReverseOptionsFailClosedWhenResolvedProviderIsUnavailable(t *testing.T) {
	contract := plugin.ContractRef{Name: "gateway.dns", Version: "v1"}
	providerID := plugin.Identity{ID: "webong/dns", Revision: "dns-r1"}
	consumerID := plugin.Identity{ID: "webong/email", Revision: "email-r1"}
	reverse := NewRegistry().DependencyReverseOptions(consumerID, []packagekit.Resolution{{
		Consumer: consumerID, Provider: providerID,
		Requirement: packagekit.Dependency{Contract: contract, Operation: "exchange"},
	}}, nil)
	request := plugin.Request{Contract: contract, Operation: "exchange", Payload: json.RawMessage(`{}`)}
	if err := reverse.Authorize(t.Context(), request); !errors.Is(err, plugin.ErrNotFound) {
		t.Fatalf("missing dependency did not fail closed: %v", err)
	}
}
