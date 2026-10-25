package spinner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	protocol "github.com/webong/gateway/arranger/integration"
)

type workerQueue struct {
	id          string
	fingerprint string
	delivery    protocol.GatewayDelivery
	calls       int
}

func (q *workerQueue) EnqueueGatewayIdempotent(d protocol.GatewayDelivery, id, fingerprint string, _ time.Time) (bool, error) {
	q.calls++
	if q.id == id && q.fingerprint == fingerprint {
		return true, nil
	}
	q.id, q.fingerprint, q.delivery = id, fingerprint, d
	return false, nil
}

func TestWorkerRawMIMEIngressIsAuthenticatedScopedAndIdempotent(t *testing.T) {
	const route = "a4a0df0f-f0aa-4f34-aade-fc5c8ef0b333"
	const mime = "From: sender@gmail.com\r\nTo: support@mail.yollof.com\r\nSubject: Ticket\r\n\r\nHelp"
	queue := &workerQueue{}
	planner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_internal/gateway/worker-ingress/"+route+"/authorize" || r.Header.Get("X-Gateway-Internal") != "internal-secret" {
			t.Errorf("invalid private authorization call: %s", r.URL.Path)
		}
		var claim map[string]string
		if err := json.NewDecoder(r.Body).Decode(&claim); err != nil {
			t.Fatal(err)
		}
		if claim["token"] != "worker-secret" || claim["recipient"] != "support@mail.yollof.com" {
			w.WriteHeader(403)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"callback_url": "https://zorvia.example.test/webhooks/gateway-mail",
			"headers": map[string][]string{"Content-Type": {"message/rfc822"},
				"X-Gateway-Route-Id": {route}, "X-Gateway-Event-Id": {claim["event_id"]},
				"X-Gateway-Envelope": {"ZW52ZWxvcGU="}, "X-Gateway-Signature": {"sha256=" + strings.Repeat("a", 64)}}})
	})
	handler := NewWorkerIngressHandler(planner, "internal-secret", queue)
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	request := func(token, recipient string, body []byte) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+"/email/worker-ingress/"+route, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "message/rfc822")
		req.Header.Set("X-Gateway-Mail-From", "sender@gmail.com")
		req.Header.Set("X-Gateway-Recipient", recipient)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if got := request("bad", "support@mail.yollof.com", []byte(mime)); got.StatusCode != 403 {
		t.Fatalf("bad token: %d", got.StatusCode)
	}
	if got := request("worker-secret", "other@mail.yollof.com", []byte(mime)); got.StatusCode != 403 {
		t.Fatalf("wrong recipient: %d", got.StatusCode)
	}
	first := request("worker-secret", "support@mail.yollof.com", []byte(mime))
	if first.StatusCode != 202 {
		t.Fatalf("first acceptance: %d", first.StatusCode)
	}
	second := request("worker-secret", "support@mail.yollof.com", []byte(mime))
	if second.StatusCode != 202 || queue.calls != 2 || queue.delivery.Target != "https://zorvia.example.test/webhooks/gateway-mail" || string(queue.delivery.Payload) != mime {
		t.Fatalf("durable replay mismatch: status=%d calls=%d delivery=%+v", second.StatusCode, queue.calls, queue.delivery)
	}
}
