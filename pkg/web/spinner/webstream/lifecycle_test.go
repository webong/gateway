package webstream

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bridge "github.com/webong/gateway/arranger/protocol"
)

func streamPlanner(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
	return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream, Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://source.test", Headers: event.Headers}}, nil
}

func TestUpstream204StopsReconnect(t *testing.T) {
	h := NewHandler(plannerFunc(streamPlanner), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) {
		return &http.Response{StatusCode: 204, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	}), Config{})
	r := httptest.NewRequest("GET", "/events", nil)
	r.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || w.Body.Len() != 0 || h.Metrics().Failed != 0 {
		t.Fatalf("unexpected stop response: %d %q", w.Code, w.Body.String())
	}
}

func TestSourceRequires200AndExactEventStreamMediaType(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		contentType string
		want        int
	}{
		{"parameterized SSE", 200, "text/event-stream; charset=utf-8", 200},
		{"wrong media type", 200, "text/event-stream-invalid", 502},
		{"partial response", 206, "text/event-stream", 502},
		{"redirect", 302, "text/event-stream", 502},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := NewHandler(plannerFunc(streamPlanner), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": {test.contentType}}, Body: io.NopCloser(strings.NewReader("data: ok\n\n"))}, nil
			}), Config{})
			r := httptest.NewRequest("GET", "/events", nil)
			r.Header.Set("Accept", "text/event-stream")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("status = %d, want %d", w.Code, test.want)
			}
		})
	}
}

func TestStreamSurvivesServerDeadlineAndReconnects(t *testing.T) {
	if testing.Short() {
		t.Skip("holds a real SSE connection beyond 15 seconds")
	}
	cancelled := make(chan struct{}, 2)
	h := NewHandler(plannerFunc(streamPlanner), sourceFunc(func(ctx context.Context, d bridge.GatewayDelivery) (*http.Response, error) {
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			if http.Header(d.Headers).Get("Last-Event-ID") == "42" {
				io.WriteString(writer, "id: 43\ndata: resumed\n\n")
				return
			}
			select {
			case <-time.After(16 * time.Second):
				io.WriteString(writer, "id: 42\ndata: first\n\n")
			case <-ctx.Done():
			}
			<-ctx.Done()
			cancelled <- struct{}{}
		}()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
	}), Config{HeartbeatInterval: time.Second})
	s := httptest.NewUnstartedServer(h)
	s.Config.WriteTimeout = 15 * time.Second
	s.Start()
	defer s.Close()
	client := &http.Client{Timeout: 22 * time.Second}
	get := func(id string) *http.Response {
		r, _ := http.NewRequest("GET", s.URL, nil)
		r.Header.Set("Accept", "text/event-stream")
		r.Header.Set("Last-Event-ID", id)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	first := get("")
	defer first.Body.Close()
	buffer := make([]byte, 128)
	var body strings.Builder
	for !strings.Contains(body.String(), "data: first") {
		n, err := first.Body.Read(buffer)
		body.Write(buffer[:n])
		if err != nil {
			t.Fatalf("stream ended early: %v", err)
		}
	}
	first.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("source was not cancelled")
	}
	resumed := get("42")
	defer resumed.Body.Close()
	data, err := io.ReadAll(resumed.Body)
	if err != nil || !strings.Contains(string(data), "id: 43") {
		t.Fatalf("resume failed: %s %v", data, err)
	}
}

func TestSlowClientWriteDeadline(t *testing.T) {
	released := make(chan struct{})
	h := NewHandler(plannerFunc(streamPlanner), sourceFunc(func(ctx context.Context, _ bridge.GatewayDelivery) (*http.Response, error) {
		reader, writer := io.Pipe()
		go func() {
			defer close(released)
			defer writer.Close()
			chunk := strings.Repeat("x", 32768)
			for {
				if _, err := io.WriteString(writer, chunk); err != nil {
					return
				}
			}
		}()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
	}), Config{WriteTimeout: 100 * time.Millisecond})
	s := httptest.NewServer(h)
	defer s.Close()
	conn, err := net.Dial("tcp", s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\nAccept: text/event-stream\r\n\r\n")
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("stalled client retained source")
	}
}
