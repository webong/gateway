package integrationextensions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	integrationcontract "github.com/webong/gateway/arranger/integration"
)

func TestRegistryHTTPSelectionFailsClosed(t *testing.T) {
	registry := NewRegistry()
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/hooks/example", nil)
	response := httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing provider returned %d", response.Code)
	}

	registry.mounted["example/web"] = mounted{http: []httpMount{{prefix: "/", proxy: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	})}}}
	response = httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("selected provider returned %d", response.Code)
	}

	registry.mounted["example/other"] = mounted{http: []httpMount{{prefix: "/", proxy: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}}}
	response = httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("ambiguous providers returned %d", response.Code)
	}
}

func TestRegistryRoutesAdvertisedDeliveryAdaptersThroughTheExtensionProcess(t *testing.T) {
	descriptor := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/mail", Revision: "r1"}, Contracts: []plugin.Contract{{
		ContractRef: integrationcontract.DeliveryAdapterContract,
		Operations:  []plugin.Operation{{Name: integrationcontract.OperationDeliverAdapter}},
	}}}
	response, err := json.Marshal(integrationcontract.DeliveryAdapterResponse{Failure: integrationcontract.AdapterFailurePermanent})
	if err != nil {
		t.Fatal(err)
	}
	process := &dependencyProcess{descriptor: descriptor, response: response}
	registry := NewRegistry()
	registry.register("example/mail", mounted{identity: descriptor.Identity, process: process, adapters: map[string]struct{}{"smtp": {}}})
	adapter, ok := registry.ResolveDeliveryAdapter("smtp")
	if !ok || len(registry.DeliveryAdapterNames()) != 1 || registry.DeliveryAdapterNames()[0] != "smtp" {
		t.Fatal("advertised delivery adapter was not discoverable")
	}
	delivery := integrationcontract.GatewayDelivery{Protocol: integrationcontract.ProtocolSMTP, Target: "buyer@example.test", Payload: []byte("message")}
	if err := adapter.DeliverGateway(context.Background(), delivery); !errors.Is(err, integrationcontract.ErrPermanentDelivery) {
		t.Fatalf("extension failure classification was not preserved: %v", err)
	}
	if process.called.Contract != integrationcontract.DeliveryAdapterContract || process.called.Operation != integrationcontract.OperationDeliverAdapter {
		t.Fatalf("unexpected Extension call: %+v", process.called)
	}

	registry.register("example/other", mounted{identity: plugin.Identity{ID: "example/other", Revision: "r1"}, process: process, adapters: map[string]struct{}{"smtp": {}}})
	if _, ok := registry.ResolveDeliveryAdapter("smtp"); ok || len(registry.DeliveryAdapterNames()) != 0 {
		t.Fatal("ambiguous delivery adapters did not fail closed")
	}
}

func TestRegistryRoutesAdvertisedNativeJobsThroughTheExtensionProcess(t *testing.T) {
	descriptor := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/mail", Revision: "r1"}, Contracts: []plugin.Contract{{
		ContractRef: integrationcontract.JobContract,
		Operations:  []plugin.Operation{{Name: integrationcontract.OperationRunJob}},
	}}}
	process := &dependencyProcess{descriptor: descriptor, response: json.RawMessage(`{}`)}
	registry := NewRegistry()
	job := integrationcontract.RuntimeJob{Name: "imap", Language: "native.imap"}
	registry.register("example/mail", mounted{identity: descriptor.Identity, process: process, jobs: map[string]integrationcontract.RuntimeJob{"native.imap": job}})
	if names := registry.NativeJobNames(); len(names) != 1 || names[0] != "imap" {
		t.Fatalf("advertised native jobs = %v", names)
	}
	if err := registry.RunNativeJob(context.Background(), "native.imap", "poll-1", map[string]any{"limit": 3}); err != nil {
		t.Fatal(err)
	}
	if process.called.Contract != integrationcontract.JobContract || process.called.Operation != integrationcontract.OperationRunJob {
		t.Fatalf("unexpected native job call: %+v", process.called)
	}
	var request integrationcontract.JobRequest
	if err := json.Unmarshal(process.called.Payload, &request); err != nil || request.Name != "imap" || request.Source != "poll-1" {
		t.Fatalf("unexpected native job request: %+v, %v", request, err)
	}

	registry.register("example/other", mounted{identity: plugin.Identity{ID: "example/other", Revision: "r1"}, process: process, jobs: map[string]integrationcontract.RuntimeJob{"native.imap": job}})
	if names := registry.NativeJobNames(); len(names) != 0 {
		t.Fatalf("ambiguous native job remained advertised: %v", names)
	}
	if err := registry.RunNativeJob(context.Background(), "native.imap", "poll-1", nil); err == nil {
		t.Fatal("ambiguous native job was executed")
	}
}

func TestRegistryRoutesFailureFollowUpsThroughTheOwningExtension(t *testing.T) {
	descriptor := plugin.Descriptor{APIVersion: plugin.APIVersion, Identity: plugin.Identity{ID: "example/mail", Revision: "r1"}, Contracts: []plugin.Contract{{
		ContractRef: integrationcontract.DeliveryFailureContract,
		Operations:  []plugin.Operation{{Name: integrationcontract.OperationDeliveryFailure}},
	}}}
	followUp := integrationcontract.GatewayDelivery{Protocol: integrationcontract.ProtocolHTTP, Adapter: "http", Target: "callback.example"}
	response, err := json.Marshal(integrationcontract.DeliveryFailureResponse{FollowUp: &followUp})
	if err != nil {
		t.Fatal(err)
	}
	process := &dependencyProcess{descriptor: descriptor, response: response}
	registry := NewRegistry()
	registry.register("example/mail", mounted{identity: descriptor.Identity, process: process, failures: map[string]struct{}{"example.adapter": {}}})
	handler, ok := registry.ResolveDeliveryFailureHandler("example.adapter")
	if !ok {
		t.Fatal("advertised failure handler was not discoverable")
	}
	result, err := handler.FollowUp(context.Background(), integrationcontract.DeliveryFailureRequest{
		ID:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Delivery: integrationcontract.GatewayDelivery{Protocol: integrationcontract.ProtocolHTTP, Adapter: "example.adapter", Target: "primary.example"},
	})
	if err != nil || result == nil || result.Target != "callback.example" {
		t.Fatalf("failure handler result: %+v %v", result, err)
	}
	if process.called.Contract != integrationcontract.DeliveryFailureContract || process.called.Operation != integrationcontract.OperationDeliveryFailure {
		t.Fatalf("unexpected failure handler call: %+v", process.called)
	}

	registry.register("example/other", mounted{identity: plugin.Identity{ID: "example/other", Revision: "r1"}, process: process, failures: map[string]struct{}{"example.adapter": {}}})
	if _, ok := registry.ResolveDeliveryFailureHandler("example.adapter"); ok {
		t.Fatal("ambiguous failure handlers did not fail closed")
	}
}

func TestRegistrySelectsTheLongestHTTPMount(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterHTTP("example/web", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	})); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterHTTPMount("example/email", "/email", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusCreated)
	})); err != nil {
		t.Fatal(err)
	}

	for requestPath, expected := range map[string]int{
		"/hooks/example": http.StatusAccepted,
		"/email":         http.StatusCreated,
		"/email/worker":  http.StatusCreated,
		"/emails":        http.StatusAccepted,
	} {
		response := httptest.NewRecorder()
		registry.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://gateway.test"+requestPath, nil))
		if response.Code != expected {
			t.Fatalf("%s selected status %d, want %d", requestPath, response.Code, expected)
		}
	}
}

func TestRegistryKeepsPublicAndPrivateHTTPMountsIsolated(t *testing.T) {
	registry := NewRegistry()
	registry.mounted["example/email"] = mounted{
		http: []httpMount{{prefix: "/email", proxy: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusAccepted)
		})}},
		private: []httpMount{{prefix: "/email", proxy: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusCreated)
		})}},
	}
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/email/submissions", nil)
	publicResponse := httptest.NewRecorder()
	registry.Handler().ServeHTTP(publicResponse, request)
	privateResponse := httptest.NewRecorder()
	registry.PrivateHandler().ServeHTTP(privateResponse, request)
	if publicResponse.Code != http.StatusAccepted || privateResponse.Code != http.StatusCreated {
		t.Fatalf("mount isolation: public=%d private=%d", publicResponse.Code, privateResponse.Code)
	}
}
