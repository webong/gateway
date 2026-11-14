package integrationextensions

import (
	"context"
	"encoding/json"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/integration"
	"testing"
	"time"
)

type legacyDurableExecutor struct{}

func (legacyDurableExecutor) Deliver(context.Context, integration.Delivery) (integration.Response, error) {
	return integration.Response{}, nil
}
func (legacyDurableExecutor) Enqueue(integration.Delivery) bool { return true }
func (legacyDurableExecutor) DeliverGateway(context.Context, integration.GatewayDelivery) error {
	return nil
}
func (legacyDurableExecutor) EnqueueGateway(integration.GatewayDelivery) bool { return true }
func (legacyDurableExecutor) DurableGatewayDeliveries() bool                  { return true }

type reservedDurableExecutor struct {
	legacyDurableExecutor
	enabled bool
}

func (p reservedDurableExecutor) IdempotentGatewayDeliveries() bool { return p.enabled }
func (reservedDurableExecutor) EnqueueGatewayIdempotent(integration.GatewayDelivery, string, string, time.Time) (bool, error) {
	return false, nil
}

func TestReservedReceiptCapabilityIsExplicitAndPreservesLegacyDurableShape(t *testing.T) {
	host := NewHost()
	host.Configure(Services{Executor: legacyDurableExecutor{}})
	request := plugin.Request{Contract: integration.DeliveryContract, Operation: integration.OperationDurableIdempotent}
	if host.authorize(plugin.Identity{}, request) == nil {
		t.Fatal("legacy durable executor advertised reserved receipts")
	}
	request.Operation = integration.OperationDurable
	raw, err := host.handle(context.Background(), plugin.Identity{}, request)
	if err != nil || string(raw) != `{"enabled":true}` {
		t.Fatalf("legacy strict response changed: %s %v", raw, err)
	}
	for _, enabled := range []bool{false, true} {
		host.Configure(Services{Executor: reservedDurableExecutor{enabled: enabled}})
		request.Operation = integration.OperationDurableIdempotent
		if err := host.authorize(plugin.Identity{}, request); err != nil {
			t.Fatal(err)
		}
		raw, err := host.handle(context.Background(), plugin.Identity{}, request)
		var result integration.DurableResponse
		if err != nil || json.Unmarshal(raw, &result) != nil || result.Enabled != enabled {
			t.Fatalf("reserved receipt capability: %s %v", raw, err)
		}
	}
}

func TestAdapterReadinessFencesSameIdentityReplacementAndAmbiguousProviders(t *testing.T) {
	registry := NewRegistry()
	identity := plugin.Identity{ID: "webong/gateway-weaver", Revision: "same-package", Version: "1.0.0"}
	old := &stopProbe{}
	next := &stopProbe{}
	registry.register(identity.ID, mounted{identity: identity, process: &runtimeProcess{extensionProcess: old}, adapters: map[string]struct{}{"weaver.work": {}}})
	if !registry.ownsDeliveryAdapter(identity, old, "weaver.work") || registry.ownsDeliveryAdapter(identity, next, "weaver.work") {
		t.Fatal("unmounted replacement acquired old process's adapter readiness")
	}
	registry.register(identity.ID, mounted{identity: identity, process: &runtimeProcess{extensionProcess: next}, adapters: map[string]struct{}{"weaver.work": {}}})
	if registry.ownsDeliveryAdapter(identity, old, "weaver.work") || !registry.ownsDeliveryAdapter(identity, next, "weaver.work") {
		t.Fatal("superseded process retained adapter readiness")
	}
	registry.register("other", mounted{identity: plugin.Identity{ID: "example/other", Revision: "r1"}, process: &stopProbe{}, adapters: map[string]struct{}{"weaver.work": {}}})
	if registry.ownsDeliveryAdapter(identity, next, "weaver.work") {
		t.Fatal("ambiguous provider remained ready")
	}
	registry.Unregister("other")
	registry.Unregister(identity.ID)
	if registry.ownsDeliveryAdapter(identity, next, "weaver.work") {
		t.Fatal("inactive provider remained ready")
	}
}

// Implementing the optional interface is the explicit host opt-in.
type pinnedHTTPExecutor struct {
	legacyDurableExecutor
	calls *int
}

func (p pinnedHTTPExecutor) DeliverPinnedHTTP(context.Context, integration.Delivery) (integration.Response, error) {
	*p.calls++
	return integration.Response{StatusCode: 204}, nil
}

func TestPinnedHTTPCapabilityRequiresExplicitExecutor(t *testing.T) {
	host := NewHost()
	request := plugin.Request{Contract: integration.DeliveryContract, Operation: integration.OperationSupportsPinnedHTTP}
	host.Configure(Services{Executor: legacyDurableExecutor{}})
	raw, err := host.handle(context.Background(), plugin.Identity{}, request)
	if err != nil || string(raw) != `{"enabled":false}` {
		t.Fatalf("legacy capability: %s %v", raw, err)
	}
	request.Operation = integration.OperationDeliverPinnedHTTP
	if host.authorize(plugin.Identity{}, request) == nil {
		t.Fatal("legacy executor acquired pinned delivery")
	}
	calls := 0
	host.Configure(Services{Executor: pinnedHTTPExecutor{calls: &calls}})
	request.Operation = integration.OperationSupportsPinnedHTTP
	raw, err = host.handle(context.Background(), plugin.Identity{}, request)
	if err != nil || string(raw) != `{"enabled":true}` {
		t.Fatalf("opt-in capability: %s %v", raw, err)
	}
	request.Operation = integration.OperationDeliverPinnedHTTP
	if err := host.authorize(plugin.Identity{}, request); err != nil {
		t.Fatal(err)
	}
	request.Payload = json.RawMessage(`{"protocol":"http","url":"https://example.com/","method":"POST"}`)
	if _, err := host.handle(context.Background(), plugin.Identity{}, request); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("pinned executor calls: %d", calls)
	}
}
