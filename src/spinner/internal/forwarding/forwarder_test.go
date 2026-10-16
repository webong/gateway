package forwarding

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/webong/gateway/src/spinner/internal/config"
	"github.com/webong/gateway/src/spinner/internal/logging"
)

func TestPostStreamPreservesBodyAndDoesNotRedirect(t *testing.T) {
	for _, body := range []string{"", "{\"input\":\"hello\"}"} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				data, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || string(data) != body || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Remove") != "" {
					t.Errorf("bad source request: %s %q %v", r.Method, data, r.Header)
				}
				http.Redirect(w, r, "/redirected", 307)
			}))
			defer s.Close()
			f := NewForwarder(&config.Config{RequestTimeout: time.Second}, logging.NewLogger("error"))
			f.addressAllowed = func(net.IP) bool { return true }
			defer f.CloseIdleConnections()
			response, err := f.OpenStreamRequest(context.Background(), "POST", s.URL, http.Header{"Content-Type": {"application/json"}, "Connection": {"X-Remove"}, "X-Remove": {"secret"}}, []byte(body), HTTPTransport{})
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 307 || calls != 1 {
				t.Fatal("POST was redirected")
			}
		})
	}
}

func TestPostStreamIsNotReplayable(t *testing.T) {
	f := NewForwarder(&config.Config{RequestTimeout: time.Second}, logging.NewLogger("error"))
	defer f.CloseIdleConnections()
	f.client.Transport = streamRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.GetBody != nil || r.Body == nil || r.Body == http.NoBody {
			t.Error("POST is replayable")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	for _, body := range []string{"", "hello"} {
		r, err := f.OpenStreamRequest(context.Background(), "POST", "https://source.test", http.Header{"Idempotency-Key": {"key"}}, []byte(body), HTTPTransport{})
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
	}
}

type streamRoundTripFunc func(*http.Request) (*http.Response, error)

func (f streamRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPlannerPrivateSourceGrantDoesNotApplyToOrdinaryForwarding(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: authorized\n\n")
	}))
	defer source.Close()
	f := NewForwarder(&config.Config{
		RequestTimeout:           time.Second,
		WebstreamPrivateNetworks: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
	}, logging.NewLogger("error"))
	defer f.CloseIdleConnections()
	policy := HTTPTransport{PrivateSourceCIDRs: "127.0.0.0/8"}
	response, err := f.OpenStream(context.Background(), source.URL+"/events", nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "data: authorized\n\n" {
		t.Fatalf("private Webstream response = %q, %v", body, err)
	}
	if _, err := f.ForwardSync(ForwardRequest{TargetURL: source.URL + "/events", Method: http.MethodGet}); err == nil {
		t.Fatal("private grant leaked into ordinary webhook forwarding")
	}
	policy.HTTPProxy = "http://proxy.invalid:8080"
	if _, err := f.OpenStream(context.Background(), source.URL+"/events", nil, policy); err == nil {
		t.Fatal("private grant accepted a profile proxy")
	}
}

func TestPlannerPrivateSourceRequiresDeploymentCeiling(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: permitted\n\n")
	}))
	defer source.Close()
	policy := HTTPTransport{PrivateSourceCIDRs: "127.0.0.0/8"}
	forwarder := NewForwarder(&config.Config{RequestTimeout: time.Second}, logging.NewLogger("error"))
	defer forwarder.CloseIdleConnections()
	if _, err := forwarder.OpenStream(context.Background(), source.URL, nil, policy); err == nil {
		t.Fatal("Planner private grant bypassed missing deployment ceiling")
	}
	forwarder.privateStreamNetworks = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/16")}
	if _, err := forwarder.OpenStream(context.Background(), source.URL, nil, policy); err == nil {
		t.Fatal("Planner grant wider than deployment ceiling was accepted")
	}
	forwarder.privateStreamNetworks = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	response, err := forwarder.OpenStream(context.Background(), source.URL, nil, policy)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	policy.PrivateSourceCIDRs = "127.0.0.1/8"
	if _, err := forwarder.OpenStream(context.Background(), source.URL, nil, policy); err == nil {
		t.Fatal("noncanonical Planner CIDR was accepted")
	}
	policy.PrivateSourceCIDRs = "127.0.0.0/8"
	policy.HTTPProxy = "http://proxy.invalid:8080"
	if _, err := forwarder.OpenStream(context.Background(), source.URL, nil, policy); err == nil {
		t.Fatal("private Planner grant accepted proxy")
	}
}

func TestStreamOpeningDeadlineAndRedirectPolicy(t *testing.T) {
	f := NewForwarder(&config.Config{RequestTimeout: 50 * time.Millisecond}, logging.NewLogger("error"))
	f.addressAllowed = func(net.IP) bool { return true }
	defer f.CloseIdleConnections()
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer stalled.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := f.OpenStream(ctx, stalled.URL, nil, HTTPTransport{}); err == nil || ctx.Err() != nil {
		t.Fatalf("opening deadline not applied: %v", err)
	}
	called := make(chan struct{}, 1)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called <- struct{}{} }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer redirect.Close()
	response, err := f.OpenStream(context.Background(), redirect.URL, http.Header{"X-Api-Key": {"secret"}}, HTTPTransport{RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatal("redirect followed")
	}
	select {
	case <-called:
		t.Fatal("credential destination was contacted")
	default:
	}
}

func TestEstablishedStreamOutlivesOpeningTimeout(t *testing.T) {
	f := NewForwarder(&config.Config{RequestTimeout: time.Second}, logging.NewLogger("error"))
	f.addressAllowed = func(net.IP) bool { return true }
	defer f.CloseIdleConnections()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-time.After(150 * time.Millisecond):
			io.WriteString(w, "data: still open\n\n")
		case <-r.Context().Done():
		}
	}))
	defer source.Close()
	response, err := f.OpenStream(context.Background(), source.URL, nil, HTTPTransport{RequestTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "data: still open\n\n" {
		t.Fatalf("opening timeout affected established stream: %q %v", body, err)
	}
}

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
