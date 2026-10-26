package smtpout

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	protocol "github.com/webong/gateway/arranger/integration"
)

type secretResolverFunc func(context.Context, string) (protocol.SecretValue, error)

func (function secretResolverFunc) Resolve(ctx context.Context, reference string) (protocol.SecretValue, error) {
	return function(ctx, reference)
}

func TestSecretGatewayAdapterResolvesCurrentCredentialPerAttempt(t *testing.T) {
	calls := 0
	resolver := secretResolverFunc(func(_ context.Context, reference string) (protocol.SecretValue, error) {
		calls++
		return protocol.SecretValue{Reference: reference, Version: int64(calls),
			Value: json.RawMessage(`{"username":"relay-user","password":"relay-password"}`)}, nil
	})
	adapter, err := NewSecretGatewayAdapter(Config{Address: "relay.example.test:587", ServerName: "relay.example.test",
		TLSMode: TLSStartTLS}, "primary-relay", resolver, nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery := protocol.GatewayDelivery{Protocol: protocol.ProtocolSMTP, Adapter: "smtp", Target: "recipient@example.test",
		Attributes: map[string]string{"mail_from": "sender@example.test"}, Payload: []byte("Subject: test\r\n\r\nbody\r\n")}
	for range 2 {
		err = adapter.DeliverGateway(context.Background(), delivery)
		if err == nil || !strings.Contains(err.Error(), "SMTP") {
			t.Fatalf("unreachable relay returned unexpected error: %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("credential resolutions = %d", calls)
	}
}

func TestSecretGatewayAdapterFailsClosedForUnavailableOrMalformedCredential(t *testing.T) {
	base := Config{Address: "relay.example.test:587", ServerName: "relay.example.test", TLSMode: TLSStartTLS}
	missing, err := NewSecretGatewayAdapter(base, "primary-relay", secretResolverFunc(func(context.Context, string) (protocol.SecretValue, error) {
		return protocol.SecretValue{}, errors.New("missing")
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery := protocol.GatewayDelivery{Protocol: protocol.ProtocolSMTP, Adapter: "smtp", Target: "recipient@example.test",
		Attributes: map[string]string{"mail_from": "sender@example.test"}, Payload: []byte("Subject: test\r\n\r\nbody\r\n")}
	if err := missing.DeliverGateway(context.Background(), delivery); err == nil {
		t.Fatal("missing secret was accepted")
	}
	malformed, err := NewSecretGatewayAdapter(base, "primary-relay", secretResolverFunc(func(_ context.Context, reference string) (protocol.SecretValue, error) {
		return protocol.SecretValue{Reference: reference, Version: 1, Value: json.RawMessage(`{"username":"relay-user","password":"one","oauth_token":"two"}`)}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := malformed.DeliverGateway(context.Background(), delivery); err == nil {
		t.Fatal("ambiguous secret was accepted")
	}
}
