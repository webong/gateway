package spinner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	protocol "github.com/webong/gateway/arranger/integration"
)

func TestWebstreamSourceOwnsPrivateNetworkCeilingAndHeaders(t *testing.T) {
	received := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		clone := request.Clone(request.Context())
		clone.Body = nil
		received <- clone
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: ready\n\n")
	}))
	t.Cleanup(server.Close)
	source, err := NewWebstreamSource([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	delivery := protocol.GatewayDelivery{
		Protocol: protocol.ProtocolHTTP, Target: server.URL,
		Headers:    map[string][]string{"Authorization": {"Bearer client"}, "Connection": {"X-Remove"}, "X-Remove": {"secret"}},
		Transport:  &protocol.HTTPTransport{StreamHeaders: map[string][]string{"Authorization": {"Bearer source"}}},
		Attributes: map[string]string{"private_source_cidrs": "127.0.0.0/8"},
	}
	response, err := source.OpenWebstream(context.Background(), delivery)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	request := <-received
	if request.Header.Get("Authorization") != "Bearer source" || request.Header.Get("X-Remove") != "" || request.Header.Get("Accept") != "text/event-stream" {
		t.Fatalf("unexpected Webstream source headers: %v", request.Header)
	}

	denied, err := NewWebstreamSource([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := denied.OpenWebstream(context.Background(), delivery); err == nil || !strings.Contains(err.Error(), "outside Extension configuration") {
		t.Fatalf("private Planner grant outside Extension configuration returned %v", err)
	}
}

func TestWebstreamSourceRetriesOnlyOptedInIdempotentPost(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if string(body) != "input" || request.Header.Get("Idempotency-Key") != "request-1" {
			t.Errorf("retry changed body or key: %q %v", body, request.Header)
		}
		if calls.Add(1) == 1 {
			connection, _, _ := writer.(http.Hijacker).Hijack()
			_ = connection.Close()
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: done\n\n")
	}))
	t.Cleanup(server.Close)
	source, _ := NewWebstreamSource([]string{"127.0.0.0/8"})
	delivery := protocol.GatewayDelivery{
		Protocol: protocol.ProtocolHTTP, Target: server.URL, Payload: []byte("input"),
		Headers: map[string][]string{"Idempotency-Key": {"request-1"}},
		Attributes: map[string]string{
			"method": "POST", "retry_idempotent": "true", "private_source_cidrs": "127.0.0.0/8",
		},
	}
	response, err := source.OpenWebstream(context.Background(), delivery)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if calls.Load() != 2 {
		t.Fatalf("Webstream retry calls = %d", calls.Load())
	}
	delivery.Headers = nil
	if _, err := source.OpenWebstream(context.Background(), delivery); err == nil {
		t.Fatal("retry without idempotency key was accepted")
	}
}

func TestWebstreamSourceDoesNotFollowRedirectAndEstablishedStreamOutlivesOpeningTimeout(t *testing.T) {
	redirected := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected <- struct{}{} }))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	source, _ := NewWebstreamSource([]string{"127.0.0.0/8"})
	delivery := protocol.GatewayDelivery{Protocol: protocol.ProtocolHTTP, Target: redirect.URL, Attributes: map[string]string{"private_source_cidrs": "127.0.0.0/8"}}
	response, err := source.OpenWebstream(context.Background(), delivery)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("redirect status = %d", response.StatusCode)
	}
	select {
	case <-redirected:
		t.Fatal("credential destination redirect was followed")
	default:
	}

	stream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		select {
		case <-time.After(120 * time.Millisecond):
			_, _ = io.WriteString(writer, "data: still-open\n\n")
		case <-request.Context().Done():
		}
	}))
	t.Cleanup(stream.Close)
	delivery.Target = stream.URL
	delivery.Transport = &protocol.HTTPTransport{RequestTimeoutSeconds: 0.05}
	response, err = source.OpenWebstream(context.Background(), delivery)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "data: still-open\n\n" {
		t.Fatalf("established stream = %q, %v", body, err)
	}
}

func TestWebstreamSourceRejectsMalformedCeilingAndPrivateProxy(t *testing.T) {
	if _, err := NewWebstreamSource([]string{"127.0.0.1/8"}); err == nil {
		t.Fatal("unmasked private-network ceiling was accepted")
	}
	source, _ := NewWebstreamSource([]string{"127.0.0.0/8"})
	delivery := protocol.GatewayDelivery{
		Protocol: protocol.ProtocolHTTP, Target: "http://127.0.0.1:8080",
		Attributes: map[string]string{"private_source_cidrs": "127.0.0.0/8"},
		Transport:  &protocol.HTTPTransport{Proxy: &protocol.HTTPProxy{HTTP: "http://proxy.example.test:8080"}},
	}
	if _, err := source.OpenWebstream(context.Background(), delivery); err == nil || !strings.Contains(err.Error(), "cannot use an HTTP proxy") {
		t.Fatalf("private source proxy returned %v", err)
	}
}
