package transport

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddedRouterConfiguresPrivateDeliveryAdminHandler(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "internal-secret")
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", t.TempDir())
	t.Setenv("GATEWAY_DELIVERY_ADMIN_ADDR", "127.0.0.1:5052")
	t.Setenv("GATEWAY_PROVISIONING_ENABLED", "false")
	spinner, err := NewRouterFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(spinner.Close)
	if spinner.DeliveryAdminServer == nil || spinner.DeliveryAdminServer.Addr != "127.0.0.1:5052" {
		t.Fatal("embedded Router did not configure the separate admin listener")
	}
	request := httptest.NewRequest(http.MethodGet, "/deliveries", nil)
	response := httptest.NewRecorder()
	spinner.DeliveryAdminServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated delivery list returned %d", response.Code)
	}
	request.Header.Set("Authorization", "Bearer internal-secret")
	response = httptest.NewRecorder()
	spinner.DeliveryAdminServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated delivery list returned %d: %s", response.Code, response.Body.String())
	}
}

func TestBundledMailboxSMTPEnablesPrivateEmailSubmission(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("MAX_WORKERS", "1")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "internal-secret")
	t.Setenv("GATEWAY_DELIVERY_SPOOL_PATH", t.TempDir())
	t.Setenv("GATEWAY_DELIVERY_ADMIN_ADDR", "127.0.0.1:5052")
	t.Setenv("GATEWAY_MAILBOX_SMTP_RESOLVER_URL", "http://127.0.0.1:5053")
	spinner, err := NewRouterFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(spinner.Close)
	input := map[string]any{
		"receipt_id": "11111111111111111111111111111111",
		"mail_from":  "alice@example.test", "authorized_senders": []string{"alice@example.test"},
		"recipients":            []string{"bob@example.test"},
		"message":               []byte("From: alice@example.test\r\nTo: bob@example.test\r\n\r\nhello"),
		"mailbox_connection_id": "11111111-1111-4111-8111-111111111111",
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/email/submissions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer internal-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	spinner.DeliveryAdminServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("bundled mailbox submission returned %d: %s", response.Code, response.Body.String())
	}
}
