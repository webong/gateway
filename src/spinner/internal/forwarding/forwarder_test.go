package forwarding

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/webong/gateway/src/spinner/internal/config"
	"github.com/webong/gateway/src/spinner/internal/logging"
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

func TestForwardStatusDoesNotTreatLargeCallbackResponseAsDeliveryFailure(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write(make([]byte, 4096))
	}))
	defer target.Close()
	forwarder := NewForwarder(&config.Config{
		MaxBodySize: 16, RequestTimeout: time.Second, IdleConnTimeout: time.Second,
		MaxIdleConns: 1, MaxConnsPerHost: 1,
	}, logging.NewLogger("error"))
	forwarder.addressAllowed = func(net.IP) bool { return true }
	t.Cleanup(forwarder.CloseIdleConnections)
	status, err := forwarder.ForwardStatus(ForwardRequest{TargetURL: target.URL, Method: http.MethodPost})
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("expected accepted status without buffering body, got %d: %v", status, err)
	}
	if _, err := forwarder.ForwardSync(ForwardRequest{TargetURL: target.URL, Method: http.MethodPost}); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("bounded body inspection should identify an oversized response, got %v", err)
	}
}

func TestForwarderUsesPerDeliveryHTTPProxy(t *testing.T) {
	proxied := make(chan *http.Request, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied <- r.Clone(r.Context())
		w.WriteHeader(http.StatusAccepted)
	}))
	defer proxy.Close()

	forwarder := NewForwarder(&config.Config{
		MaxBodySize:     1024,
		RequestTimeout:  time.Second,
		IdleConnTimeout: time.Second,
		MaxIdleConns:    1,
		MaxConnsPerHost: 1,
	}, logging.NewLogger("error"))
	forwarder.addressAllowed = func(net.IP) bool { return true }
	t.Cleanup(forwarder.CloseIdleConnections)

	response, err := forwarder.ForwardSync(ForwardRequest{
		TargetURL: "http://subscriber.example.test/hook",
		Method:    http.MethodPost,
		Transport: HTTPTransport{
			HTTPProxy:      proxy.URL,
			ConnectTimeout: 250 * time.Millisecond,
			RequestTimeout: time.Second,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("expected proxy response status 202, got %d", response.StatusCode)
	}

	select {
	case request := <-proxied:
		if request.URL.Host != "subscriber.example.test" || request.URL.Path != "/hook" {
			t.Fatalf("proxy received unexpected target URL %q", request.URL.String())
		}
	case <-time.After(time.Second):
		t.Fatal("proxy did not receive delivery")
	}
}

func TestForwarderHonorsPerDeliveryNoProxyHosts(t *testing.T) {
	forwarder := NewForwarder(&config.Config{
		MaxBodySize:     1024,
		RequestTimeout:  time.Second,
		IdleConnTimeout: time.Second,
		MaxIdleConns:    1,
		MaxConnsPerHost: 1,
	}, logging.NewLogger("error"))
	t.Cleanup(forwarder.CloseIdleConnections)

	client, err := forwarder.clientFor(HTTPTransport{
		HTTPProxy: "http://proxy.example.test:8080",
		NoProxy:   []string{".internal.example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	transport := client.Transport.(*http.Transport)
	directRequest := httptest.NewRequest(http.MethodPost, "http://hooks.internal.example.test/delivery", nil)
	selected, err := transport.Proxy(directRequest)
	if err != nil {
		t.Fatal(err)
	}
	if selected != nil {
		t.Fatalf("expected no-proxy destination to connect directly, got %s", selected)
	}

	proxiedRequest := httptest.NewRequest(http.MethodPost, "http://hooks.external.example.test/delivery", nil)
	selected, err = transport.Proxy(proxiedRequest)
	if err != nil {
		t.Fatal(err)
	}
	if selected == nil || selected.String() != "http://proxy.example.test:8080" {
		t.Fatalf("expected external destination to use configured proxy, got %v", selected)
	}
}
