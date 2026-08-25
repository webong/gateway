package forwarding

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/webong/gateway/cmd/internal/config"
	"github.com/webong/gateway/cmd/internal/logging"
)

func TestForwarderMergesTargetAndWebhookQueryParameters(t *testing.T) {
	receivedQuery := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery <- r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	forwarder := NewForwarder(&config.Config{
		MaxBodySize:     1024,
		RequestTimeout:  time.Second,
		IdleConnTimeout: time.Second,
		MaxIdleConns:    1,
		MaxConnsPerHost: 1,
	}, logging.NewLogger("error"))
	forwarder.addressAllowed = func(net.IP) bool { return true }

	response, err := forwarder.ForwardSync(ForwardRequest{
		TargetURL: target.URL + "/callback?token=abc#fragment",
		Method:    http.MethodGet,
		RawQuery:  "source=meta",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 response, got %d", response.StatusCode)
	}
	if query := <-receivedQuery; query != "token=abc&source=meta" {
		t.Fatalf("expected merged query, got %q", query)
	}
}
