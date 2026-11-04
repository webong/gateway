package secret

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/integration"
)

func TestReverseSecretLookupIsScopedToInstalledIdentity(t *testing.T) {
	host := NewHost()
	host.Configure(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/_internal/gateway/extensions/webong/email/secrets/relay" ||
			request.Header.Get("X-Gateway-Internal") != "private-token" {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(integration.SecretValue{
			Reference: "relay", Version: 1, Value: json.RawMessage(`{"password":"secret"}`),
		})
	}), "private-token")
	payload, _ := json.Marshal(integration.SecretRequest{Reference: "relay"})
	request := plugin.Request{Contract: integration.SecretContract, Operation: integration.OperationResolveSecret, Payload: payload}
	owned := host.ReverseOptionsFor(plugin.Identity{ID: "webong/email", Revision: "r1"}, nil)
	if err := owned.Authorize(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	result, err := owned.Handle(context.Background(), request)
	if err != nil || !json.Valid(result) {
		t.Fatalf("own secret lookup failed: %v", err)
	}
	other := host.ReverseOptionsFor(plugin.Identity{ID: "webong/dns", Revision: "r1"}, nil)
	if _, err := other.Handle(context.Background(), request); err == nil {
		t.Fatal("cross-extension secret lookup succeeded")
	}
	request.Operation = "unknown"
	if err := owned.Authorize(context.Background(), request); err == nil {
		t.Fatal("unknown secret operation was authorized")
	}
}
