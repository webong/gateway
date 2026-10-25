package delivery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	protocol "github.com/webong/gateway/arranger/integration"
)

type fixedHostedMailResolver struct{ spec HostedMailAdapterSpec }

func (r fixedHostedMailResolver) Resolve(context.Context, string) (HostedMailAdapterSpec, error) {
	return r.spec, nil
}

type revokedHostedMailResolver struct{}

func (revokedHostedMailResolver) Resolve(context.Context, string) (HostedMailAdapterSpec, error) {
	return HostedMailAdapterSpec{}, ErrHostedMailAdapterUnavailable
}

func TestManagedHostedMailAdapterStopsRetryingRevokedAdapter(t *testing.T) {
	adapter, err := NewManagedHostedMailAdapter(revokedHostedMailResolver{}, []string{"provider.test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.DeliverGateway(context.Background(), protocol.GatewayDelivery{Protocol: protocol.ProtocolSMTP,
		Adapter: "hosted.mail", Attributes: map[string]string{"hosted_adapter_id": "provider.primary"}})
	if !errors.Is(err, protocol.ErrPermanentDelivery) {
		t.Fatalf("revoked adapter remained retryable: %v", err)
	}
}

func TestManagedHostedMailAdapterResolvesCurrentSpecAndHonorsHostCeiling(t *testing.T) {
	requests := 0
	provider := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Header.Get("Authorization") != "Bearer current-token" {
			t.Errorf("wrong provider token: %q", request.Header.Get("Authorization"))
		}
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer provider.Close()
	resolver := fixedHostedMailResolver{HostedMailAdapterSpec{URL: provider.URL, BearerToken: "current-token"}}
	delivery := protocol.GatewayDelivery{Protocol: protocol.ProtocolSMTP, Adapter: "hosted.mail",
		Target: "support", SubscriberID: "account-1", Payload: []byte("From: sender@example.test\r\n\r\nbody"),
		Attributes: map[string]string{"hosted_adapter_id": "provider.primary", "account_id": "account-1",
			"recipient": "support@example.test", "event_id": "event-1"}}
	blocked, err := NewManagedHostedMailAdapter(resolver, []string{"other.test"}, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := blocked.DeliverGateway(context.Background(), delivery); err == nil || !strings.Contains(err.Error(), "not deployed") {
		t.Fatalf("unapproved provider host accepted: %v", err)
	}
	if requests != 0 {
		t.Fatal("blocked provider received a request")
	}
	allowed, err := NewManagedHostedMailAdapter(resolver, []string{"127.0.0.1"}, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := allowed.DeliverGateway(context.Background(), delivery); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("provider requests = %d", requests)
	}
}
