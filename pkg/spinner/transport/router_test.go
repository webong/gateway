package transport

import (
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
