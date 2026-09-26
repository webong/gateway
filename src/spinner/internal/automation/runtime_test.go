package automation

import (
	"context"
	"testing"
	"time"
)

func TestJavaScriptCanSetVariablesRespondAndForward(t *testing.T) {
	outcome, err := (Runtime{}).Execute(context.Background(), Script{
		Language: LanguageJavaScript,
		Source:   `gateway.set("customer.id", gateway.event.customer.id); gateway.forward({url: "https://example.test/hook", method: "POST", body: "ok"}); gateway.respond("accepted", 202);`,
	}, Input{Event: map[string]any{"customer": map[string]any{"id": "cus_123"}}})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := getPath(outcome.Variables, "customer.id"); !ok || value != "cus_123" {
		t.Fatalf("expected customer.id, got %#v", outcome.Variables)
	}
	if outcome.Response == nil || outcome.Response.Status != 202 || outcome.Response.Body != "accepted" {
		t.Fatalf("unexpected response %#v", outcome.Response)
	}
	if len(outcome.Deliveries) != 1 || outcome.Deliveries[0].URL != "https://example.test/hook" {
		t.Fatalf("unexpected deliveries %#v", outcome.Deliveries)
	}
}

func TestWebhookScriptCompatibilityDialectUsesGatewayVariables(t *testing.T) {
	outcome, err := (Runtime{}).Execute(context.Background(), Script{
		Language: LanguageWebhookScript,
		Source:   `set('request.result', var('request.value')); respond('done', 201);`,
	}, Input{Variables: map[string]any{"request": map[string]any{"value": "ready"}}})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := getPath(outcome.Variables, "request.result"); !ok || value != "ready" {
		t.Fatalf("expected copied result, got %#v", outcome.Variables)
	}
	if outcome.Response == nil || outcome.Response.Status != 201 {
		t.Fatalf("unexpected response %#v", outcome.Response)
	}
}

func TestLuaCanUseTheSameCapabilities(t *testing.T) {
	outcome, err := (Runtime{}).Execute(context.Background(), Script{
		Language: LanguageLua,
		Source:   `gateway.set("value", gateway.event.value); gateway.respond("ok", 204)`,
	}, Input{Event: map[string]any{"value": "copied"}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Variables["value"] != "copied" || outcome.Response == nil || outcome.Response.Status != 204 {
		t.Fatalf("unexpected outcome %#v", outcome)
	}
}

func TestJavaScriptTimesOut(t *testing.T) {
	start := time.Now()
	_, err := (Runtime{Timeout: 15 * time.Millisecond}).Execute(context.Background(), Script{Language: LanguageJavaScript, Source: `while (true) {}`}, Input{})
	if err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("script deadline was not enforced promptly: %v", time.Since(start))
	}
}

func TestJavaScriptDoesNotExposeHostModules(t *testing.T) {
	outcome, err := (Runtime{}).Execute(context.Background(), Script{
		Language: LanguageJavaScript,
		Source:   `gateway.respond([typeof std, typeof os, typeof fetch, typeof process].join(','), 200);`,
	}, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Response == nil || outcome.Response.Body != "undefined,undefined,undefined,undefined" {
		t.Fatalf("unexpected host capability exposure: %#v", outcome.Response)
	}
}

func TestJavaScriptRejectsInvalidResponseStatus(t *testing.T) {
	_, err := (Runtime{}).Execute(context.Background(), Script{
		Language: LanguageJavaScript,
		Source:   `gateway.respond('bad', 999);`,
	}, Input{})
	if err == nil {
		t.Fatal("expected invalid status to fail before the edge writes a response")
	}
}
