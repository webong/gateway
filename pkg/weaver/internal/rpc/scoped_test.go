package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type principalInvoker struct{ account string }

func (p *principalInvoker) Invoke(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, *Error) {
	p.account = Principal(ctx)
	return json.RawMessage(`{}`), nil
}
func TestScopedTransportSuppliesPrincipalIndependentlyOfClientParams(t *testing.T) {
	invoker := &principalInvoker{}
	token := strings.Repeat("a", 32)
	handler, err := NewScopedHandler(invoker, map[string]string{"account-a": token})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"flow.get","principal":"attacker","params":{"account":"attacker"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 200 || invoker.account != "account-a" {
		t.Fatalf("principal=%q status=%d", invoker.account, response.Code)
	}
	invoker.account = ""
	req = httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 401 || invoker.account != "" {
		t.Fatal("invalid credential reached application")
	}
}
func TestScopedCredentialsRejectAmbiguousOrWeakTokens(t *testing.T) {
	for _, credentials := range []map[string]string{nil, {"a": "short"}, {"a": strings.Repeat("a", 32), "b": strings.Repeat("a", 32)}} {
		if _, err := NewScopedHandler(&principalInvoker{}, credentials); err == nil {
			t.Fatal("unsafe credentials accepted")
		}
	}
}
