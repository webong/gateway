package spinner

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalPlannerRequestOwnsOnlyEmailRuntimeRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodGet, "/_internal/gateway/mailbox-connections/667d5bd4-7863-4aa4-8390-195d663fa465/smtp", true},
		{http.MethodGet, "/_internal/gateway/email/dkim/example.com", true},
		{http.MethodHead, "/_internal/gateway/email/dkim/example.com", true},
		{http.MethodGet, "/_internal/gateway/email/hosted-adapters/customer.provider", true},
		{http.MethodPost, "/_internal/gateway/email/outbound-plan", true},
		{http.MethodPost, "/_internal/gateway/email/outbound-attempts/delivery-1/reserve", true},
		{http.MethodPost, "/_internal/gateway/email/outbound-attempts/delivery-1", true},
		{http.MethodGet, "/_internal/gateway/email/outbound-plan", false},
		{http.MethodGet, "/_internal/gateway/email/dkim/example.com/private", false},
		{http.MethodGet, "/_internal/gateway/extensions", false},
		{http.MethodGet, "/registry/endpoints", false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(test.method, test.path, nil)
			if got := InternalPlannerRequest(request); got != test.want {
				t.Fatalf("InternalPlannerRequest() = %v, want %v", got, test.want)
			}
		})
	}
}
