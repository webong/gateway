package integration

import (
	"context"
	"encoding/json"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
)

func TestRuntimeDNSUsesTheDeclaredReverseContract(t *testing.T) {
	caller := reverseCallerFunc(func(_ context.Context, ref plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
		if ref != DNSContract || operation != OperationDNSExchange {
			t.Fatalf("unexpected DNS operation: %s/%s %s", ref.Name, ref.Version, operation)
		}
		var request DNSExchangeRequest
		if err := json.Unmarshal(payload, &request); err != nil || string(request.Message) != "query" {
			t.Fatalf("unexpected DNS query: %q err=%v", request.Message, err)
		}
		return json.RawMessage(`{"message":"cmVzcG9uc2U="}`), nil
	})
	response, err := NewRuntimeHost(caller, nil).DNS().Exchange(t.Context(), []byte("query"))
	if err != nil || string(response) != "response" {
		t.Fatalf("unexpected DNS response: %q err=%v", response, err)
	}
}

func TestRuntimeDNSRejectsEmptyMessages(t *testing.T) {
	if _, err := NewRuntimeHost(nil, nil).DNS().Exchange(t.Context(), nil); err == nil {
		t.Fatal("empty DNS query was accepted")
	}
}
