package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/webong/gateway/driver/automation"
)

func TestJavaScriptCanSetVariablesRespondAndForward(t *testing.T) {
	runtime := automation.NewRuntime(New())
	outcome, err := runtime.Execute(context.Background(), automation.Script{
		Language: Language,
		Source:   `gateway.set("customer.id", gateway.event.customer.id); gateway.forward({url: "https://example.test/hook", method: "POST", body: "ok"}); gateway.respond("accepted", 202);`,
	}, automation.Input{Event: map[string]any{"customer": map[string]any{"id": "cus_123"}}})
	if err != nil {
		t.Fatal(err)
	}
	customer, _ := outcome.Variables["customer"].(map[string]any)
	if customer["id"] != "cus_123" {
		t.Fatalf("expected customer.id, got %#v", outcome.Variables)
	}
	if outcome.Response == nil || outcome.Response.Status != 202 || outcome.Response.Body != "accepted" {
		t.Fatalf("unexpected response %#v", outcome.Response)
	}
	if len(outcome.Deliveries) != 1 || outcome.Deliveries[0].URL != "https://example.test/hook" {
		t.Fatalf("unexpected deliveries %#v", outcome.Deliveries)
	}
}

func TestJavaScriptTimesOut(t *testing.T) {
	engine := New()
	engine.Timeout = 15 * time.Millisecond
	start := time.Now()
	_, err := engine.Execute(context.Background(), `while (true) {}`, automation.Input{})
	if err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("script deadline was not enforced promptly: %v", time.Since(start))
	}
}

func TestJavaScriptDoesNotExposeHostModules(t *testing.T) {
	outcome, err := automation.NewRuntime(New()).Execute(context.Background(), automation.Script{
		Language: Language,
		Source:   `gateway.respond([typeof std, typeof os, typeof fetch, typeof process].join(','), 200);`,
	}, automation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Response == nil || outcome.Response.Body != "undefined,undefined,undefined,undefined" {
		t.Fatalf("unexpected host capability exposure: %#v", outcome.Response)
	}
}

func TestJavaScriptRejectsInvalidResponseStatus(t *testing.T) {
	_, err := automation.NewRuntime(New()).Execute(context.Background(), automation.Script{
		Language: Language,
		Source:   `gateway.respond('bad', 999);`,
	}, automation.Input{})
	if err == nil {
		t.Fatal("expected invalid status to fail before the edge writes a response")
	}
}
