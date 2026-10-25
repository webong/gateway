package webstream

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bridge "github.com/webong/gateway/arranger/integration"
)

func TestPostStreamsBeforeCompletionAndCancelsSource(t *testing.T) {
	cancelled := make(chan struct{})
	h := NewHandler(plannerFunc(func(_ context.Context, e bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream, Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://source.test", Attributes: map[string]string{"method": "POST"}, Payload: e.Payload}}, nil
	}), sourceFunc(func(ctx context.Context, _ bridge.GatewayDelivery) (*http.Response, error) {
		r, w := io.Pipe()
		go func() { defer w.Close(); io.WriteString(w, "data: first\n\n"); <-ctx.Done(); close(cancelled) }()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: r}, nil
	}), Config{})
	s := httptest.NewServer(h)
	defer s.Close()
	r, _ := http.NewRequest("POST", s.URL, strings.NewReader("input"))
	r.Header.Set("Accept", "text/event-stream")
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("not streamed: %q %v", line, err)
	}
	response.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("POST source not cancelled")
	}
}

func TestPostBodyAuthorizationAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name, body, method string
		reject             bool
		want               int
		planned, opened    bool
	}{
		{"post", "{\"input\":\"hello\"}", "POST", false, 200, true, true},
		{"empty", "", "POST", false, 200, true, true},
		{"too large", strings.Repeat("x", 33), "POST", false, 413, false, false},
		{"unauthorized", "secret", "POST", true, 403, true, false},
		{"missing method approval", "secret", "", false, 502, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planned, opened := false, false
			h := NewHandler(plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
				planned = true
				if event.Method != "POST" || string(event.Payload) != tc.body {
					t.Errorf("request was changed: %#v", event)
				}
				if tc.reject {
					return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayReject, StatusCode: 403}, nil
				}
				return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream, Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://source.test", Attributes: map[string]string{"method": tc.method}, Payload: event.Payload}}, nil
			}), sourceFunc(func(_ context.Context, d bridge.GatewayDelivery) (*http.Response, error) {
				opened = true
				if string(d.Payload) != tc.body {
					t.Error("source body changed")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: done\n\n"))}, nil
			}), Config{MaxBodySize: 32})
			r := httptest.NewRequest("POST", "/events", strings.NewReader(tc.body))
			r.ContentLength = -1 // Exercise unknown-length/chunked limits too.
			r.Header.Set("Accept", "text/event-stream")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || planned != tc.planned || opened != tc.opened {
				t.Fatalf("status=%d planned=%v opened=%v", w.Code, planned, opened)
			}
			if strings.Contains(w.Body.String(), "retry:") {
				t.Fatal("POST received an automatic retry hint")
			}
		})
	}
}

func TestPostSlowUploadTimesOutBeforePlanning(t *testing.T) {
	h := NewHandler(plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		t.Error("planned incomplete upload")
		return bridge.GatewayDecision{}, nil
	}), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) {
		t.Error("opened incomplete upload")
		return nil, nil
	}), Config{BodyReadTimeout: 50 * time.Millisecond})
	s := httptest.NewServer(h)
	defer s.Close()
	conn, err := net.Dial("tcp", s.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	io.WriteString(conn, "POST / HTTP/1.1\r\nHost: test\r\nAccept: text/event-stream\r\nContent-Length: 100\r\n\r\nx")
	data, err := io.ReadAll(conn)
	if err != nil || !strings.Contains(string(data), "408") {
		t.Fatalf("slow upload: %q %v", data, err)
	}
}
