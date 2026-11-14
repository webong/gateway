package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeInvoker struct {
	calls int
}

func (f *fakeInvoker) Invoke(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, *Error) {
	f.calls++
	if method != "flow.get" {
		return nil, &Error{Code: -32601, Message: "Method not found"}
	}
	return json.RawMessage(`{"status":"completed"}`), nil
}

func request(body string, token string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestHandler(t *testing.T) {
	invoker := &fakeInvoker{}
	handler, err := NewHandler(invoker, "secret")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		body       string
		token      string
		status     int
		contains   string
		callChange int
	}{
		{"valid request", `{"jsonrpc":"2.0","method":"flow.get","params":{},"id":7}`, "secret", 200, `"result":{"status":"completed"}`, 1},
		{"unauthorized", `{"jsonrpc":"2.0","method":"flow.get","id":7}`, "wrong", 401, "Unauthorized", 0},
		{"parse error", `{`, "secret", 200, `"code":-32700`, 0},
		{"invalid params", `{"jsonrpc":"2.0","method":"flow.get","params":[],"id":7}`, "secret", 200, `"code":-32602`, 0},
		{"invalid id", `{"jsonrpc":"2.0","method":"flow.get","id":{}}`, "secret", 200, `"code":-32600`, 0},
		{"notification", `{"jsonrpc":"2.0","method":"flow.get"}`, "secret", 204, "", 1},
		{"start notification does not mutate", `{"jsonrpc":"2.0","method":"flow.start"}`, "secret", 204, "", 0},
		{"completion notification does not mutate", `{"jsonrpc":"2.0","method":"flow.complete"}`, "secret", 204, "", 0},
		{"resume notification does not mutate", `{"jsonrpc":"2.0","method":"flow.resume"}`, "secret", 204, "", 0},
		{"cancel notification does not mutate", `{"jsonrpc":"2.0","method":"flow.cancel"}`, "secret", 204, "", 0},
		{"batch", `[{"jsonrpc":"2.0","method":"flow.get","id":"one"},{"jsonrpc":"2.0","method":"unknown","id":"two"}]`, "secret", 200, `"code":-32601`, 2},
		{"empty batch", `[]`, "secret", 200, `"code":-32600`, 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := invoker.calls
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, request(test.body, test.token))
			if w.Code != test.status || !strings.Contains(w.Body.String(), test.contains) || invoker.calls-before != test.callChange {
				t.Fatalf("status=%d body=%q calls=%d", w.Code, w.Body.String(), invoker.calls-before)
			}
		})
	}
}

func TestHandlerRequiresAuthentication(t *testing.T) {
	if _, err := NewHandler(&fakeInvoker{}, ""); err == nil {
		t.Fatal("expected an error without a bearer token")
	}
}
