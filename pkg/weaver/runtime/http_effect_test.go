package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	arranger "github.com/webong/gateway/arranger/integration"
)

func TestHTTPDestinationUsesGatewayAndNeverClientSelectedURL(t *testing.T) {
	destination, err := parseHTTPDestination(json.RawMessage(`{"url":"https://provider.example/fulfil","effect_scope":"provider-account-1","headers":{"Authorization":["Bearer private-provider"]},"idempotent":true}`))
	if err != nil {
		t.Fatal(err)
	}
	service := New(t.TempDir())
	service.controlToken = "private-control"
	service.destinations = map[string]httpDestination{"orders": destination}
	calls := 0
	caller := reverseFunc(func(_ context.Context, ref plugin.ContractRef, operation string, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		if ref != arranger.DeliveryContract || operation != arranger.OperationDeliverPinnedHTTP {
			t.Fatal("provider bypassed Gateway HTTP delivery")
		}
		var delivery arranger.Delivery
		if err := json.Unmarshal(raw, &delivery); err != nil {
			return nil, err
		}
		if delivery.URL != "https://provider.example/fulfil" || delivery.Method != "POST" || delivery.Headers["Idempotency-Key"][0] != strings.Repeat("a", 64) {
			t.Fatalf("untrusted provider request: %+v", delivery)
		}
		return json.RawMessage(`{"response":{"status_code":204,"headers":{"Authorization":["secret-response"]},"body":"c2VjcmV0"}}`), nil
	})
	handler := service.effectHandler(caller)
	invoke := func(body string, token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://127.0.0.1/internal/deliver", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	body := `{"destination":"orders","destination_sha256":"124e5a7238e4e87786f8b225e0d4f34c7ffe0ef8377bea00ac262c3885e070fb","idempotency_key":"` + strings.Repeat("a", 64) + `","payload":{"order":42}}`
	if response := invoke(body, "wrong"); response.Code != 401 {
		t.Fatal("private effect credential bypassed")
	}
	if response := invoke(strings.TrimSuffix(body, "}")+`,"url":"https://client.example"}`, "private-control"); response.Code != 400 {
		t.Fatal("client-selected URL accepted")
	}
	response := invoke(body, "private-control")
	if response.Code != 200 || strings.Contains(response.Body.String(), "secret") || !strings.Contains(response.Body.String(), `"status":204`) || calls != 1 {
		t.Fatalf("unsafe effect response: %s calls=%d", response.Body.String(), calls)
	}
}

func TestHTTPUnknownOutcomesRespectProviderReplayGuarantee(t *testing.T) {
	for _, idempotent := range []bool{false, true} {
		raw, _ := json.Marshal(map[string]any{"url": "https://provider.example/fulfil", "effect_scope": "provider-account-1", "idempotent": idempotent})
		destination, err := parseHTTPDestination(raw)
		if err != nil {
			t.Fatal(err)
		}
		service := New(t.TempDir())
		service.controlToken = "private-control"
		service.destinations = map[string]httpDestination{"orders": destination}
		caller := reverseFunc(func(_ context.Context, _ plugin.ContractRef, _ string, raw json.RawMessage) (json.RawMessage, error) {
			var delivery arranger.Delivery
			if err := json.Unmarshal(raw, &delivery); err != nil {
				return nil, err
			}
			if !idempotent && (delivery.Headers["Idempotency-Key"] != nil || delivery.Headers["X-Idempotency-Key"] != nil) {
				t.Fatal("non-idempotent provider advertised HTTP replay safety")
			}
			return nil, errors.New("lost provider response")
		})
		body := `{"destination":"orders","destination_sha256":"` + destination.fingerprint() + `","idempotency_key":"` + strings.Repeat("a", 64) + `","payload":{}}`
		request := httptest.NewRequest("POST", "http://127.0.0.1/internal/deliver", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer private-control")
		response := httptest.NewRecorder()
		service.effectHandler(caller).ServeHTTP(response, request)
		var result effectResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if !result.Uncertain || result.Retryable != idempotent {
			t.Fatalf("provider replay guarantee ignored: %+v", result)
		}
	}
}

func TestHTTPDestinationRejectsCredentialAndReplayHeaderInjection(t *testing.T) {
	for _, raw := range []string{
		`{"url":"https://user:password@provider.example/fulfil","effect_scope":"provider-account-1"}`,
		`{"url":"https://provider.example/fulfil","effect_scope":"provider-account-1","headers":{"Authorization":["token\r\nInjected: yes"]}}`,
		`{"url":"https://provider.example/fulfil","effect_scope":"provider-account-1","idempotent":false,"idempotency_header":"Idempotency-Key"}`,
		`{"url":"https://provider.example/fulfil","effect_scope":"provider-account-1","headers":{"Idempotency-Key":["static-key"]}}`,
		`{"url":"https://provider.example/fulfil","effect_scope":"provider-account-1","timeout_seconds":60}`,
	} {
		if _, err := parseHTTPDestination(json.RawMessage(raw)); err == nil {
			t.Fatal("unsafe destination accepted")
		}
	}
}

func TestUncertainEffectCannotReplayIntoChangedProviderScopeOrHeader(t *testing.T) {
	original, err := parseHTTPDestination(json.RawMessage(`{"url":"https://provider.example/fulfil","effect_scope":"provider-account-1","idempotent":true}`))
	if err != nil {
		t.Fatal(err)
	}
	service := New(t.TempDir())
	service.controlToken = "private-control"
	service.destinations = map[string]httpDestination{"orders": original}
	effects := 0
	caller := reverseFunc(func(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error) {
		effects++
		return nil, errors.New("effect committed but result lost")
	})
	body := `{"destination":"orders","destination_sha256":"` + original.fingerprint() + `","idempotency_key":"` + strings.Repeat("a", 64) + `","payload":{}}`
	invoke := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://127.0.0.1/internal/deliver", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer private-control")
		response := httptest.NewRecorder()
		service.effectHandler(caller).ServeHTTP(response, request)
		return response
	}
	if invoke().Code != 200 || effects != 1 {
		t.Fatal("uncertain first effect not admitted")
	}
	for _, changed := range []httpDestination{func() httpDestination { value := original; value.EffectScope = "provider-account-2"; return value }(), func() httpDestination {
		value := original
		value.IdempotencyHeader = "X-Provider-Receipt"
		return value
	}()} {
		service.destinations["orders"] = changed
		if invoke().Code != 403 || effects != 1 {
			t.Fatal("old pinned effect replayed in another deduplication domain")
		}
	}
}
