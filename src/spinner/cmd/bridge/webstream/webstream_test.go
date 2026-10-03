package webstream

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

type plannerFunc func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error)

func (f plannerFunc) PlanEvent(ctx context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
	return f(ctx, event)
}

type sourceFunc func(context.Context, bridge.GatewayDelivery) (*http.Response, error)

func (f sourceFunc) OpenWebstream(ctx context.Context, delivery bridge.GatewayDelivery) (*http.Response, error) {
	return f(ctx, delivery)
}

func TestHandlerStreamsPlannerAuthorizedEvents(t *testing.T) {
	events := make(chan bridge.GatewayEvent, 1)
	var opened bridge.GatewayDelivery
	handler := NewHandler(plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		events <- event
		return bridge.GatewayDecision{
			Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream,
			Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://stream.example.test/events"},
		}, nil
	}), sourceFunc(func(_ context.Context, delivery bridge.GatewayDelivery) (*http.Response, error) {
		opened = delivery
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(bytes.NewBufferString("event: update\ndata: hello\n\n"))}, nil
	}), Config{PlannerTimeout: time.Second, ReconnectDelay: 1500 * time.Millisecond})

	request := httptest.NewRequest(http.MethodGet, "/hooks/updates?tenant=acme", nil)
	request.Header.Set("Accept", "text/event-stream")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" || response.Body.String() != "retry: 1500\n\nevent: update\ndata: hello\n\n" {
		t.Fatalf("unexpected stream response: status=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
	if opened.Target != "https://stream.example.test/events" {
		t.Fatalf("unexpected source: %+v", opened)
	}
	select {
	case event := <-events:
		if event.Protocol != bridge.ProtocolWebstream || event.Kind != bridge.EventConnect || event.Route != "/hooks/updates" || event.RawQuery != "tenant=acme" {
			t.Fatalf("unexpected planner event: %+v", event)
		}
	default:
		t.Fatal("planner did not receive Webstream connect event")
	}
}

func TestHandlerDoesNotForwardConnectionScopedSourceHeaders(t *testing.T) {
	handler := NewHandler(plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{
			Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream,
			Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://stream.example.test/events"},
		}, nil
	}), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":      {"text/event-stream"},
				"Connection":        {"keep-alive, X-Internal", "X-Other"},
				"X-Internal":        {"upstream-only"},
				"X-Other":           {"upstream-only"},
				"X-Allowed-Through": {"yes"},
			},
			Body: io.NopCloser(bytes.NewBufferString("data: hello\n\n")),
		}, nil
	}), Config{})
	request := httptest.NewRequest(http.MethodGet, "/hooks/updates", nil)
	request.Header.Set("Accept", "text/event-stream")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-Internal") != "" || response.Header().Get("X-Other") != "" || response.Header().Get("Connection") != "" || response.Header().Get("X-Allowed-Through") != "yes" {
		t.Fatalf("unexpected source response headers: status=%d headers=%v", response.Code, response.Header())
	}
}

func TestHandlerRequiresExplicitSSEAcceptHeader(t *testing.T) {
	handler := NewHandler(plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		t.Fatal("planner should not run")
		return bridge.GatewayDecision{}, nil
	}), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) { return nil, nil }), Config{PlannerTimeout: time.Second})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/hooks/updates", nil))
	if response.Code != http.StatusNotAcceptable {
		t.Fatalf("expected 406, got %d", response.Code)
	}
}

func TestHandlerPassesLastEventIDToPlanner(t *testing.T) {
	events := make(chan bridge.GatewayEvent, 1)
	handler := NewHandler(plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		events <- event
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayReject, StatusCode: http.StatusForbidden}, nil
	}), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) { return nil, nil }), Config{})
	request := httptest.NewRequest(http.MethodGet, "/hooks/updates", nil)
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", "event-42")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if event := <-events; event.Attributes["last_event_id"] != "event-42" {
		t.Fatalf("last event ID was not planned: %+v", event)
	}
}

func TestSharedAdmissionRejectsBeforePlanningAndReleasesOnDisconnect(t *testing.T) {
	gate := NewAdmission(1)
	reader, writer := io.Pipe()
	defer writer.Close()
	opened := make(chan struct{}, 1)
	decision := bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream,
		Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://events.example.test/stream"}}
	first := NewHandler(plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return decision, nil
	}), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) {
		opened <- struct{}{}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
	}), Config{Admission: gate, MaxDuration: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/hooks/updates", nil).WithContext(ctx)
	request.Header.Set("Accept", "text/event-stream")
	done := make(chan struct{})
	go func() { first.ServeHTTP(httptest.NewRecorder(), request); close(done) }()
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("first stream did not open")
	}
	planned := false
	second := NewHandler(plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		planned = true
		return decision, nil
	}), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(bytes.NewBuffer(nil))}, nil
	}), Config{Admission: gate})
	response := httptest.NewRecorder()
	secondRequest := httptest.NewRequest(http.MethodGet, "/hooks/updates", nil)
	secondRequest.Header.Set("Accept", "text/event-stream")
	second.ServeHTTP(response, secondRequest)
	if response.Code != http.StatusServiceUnavailable || planned || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("limit did not reject before planning: status=%d planned=%v", response.Code, planned)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disconnected stream did not release its slot")
	}
	response = httptest.NewRecorder()
	second.ServeHTTP(response, secondRequest)
	if response.Code != http.StatusOK || !planned || gate.active.Load() != 0 {
		t.Fatalf("slot was not reusable: status=%d planned=%v active=%d", response.Code, planned, gate.active.Load())
	}
}

func TestMaxDurationClosesIdleWebstream(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	handler := NewHandler(plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream,
			Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://events.example.test/stream"}}, nil
	}), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
	}), Config{MaxDuration: 30 * time.Millisecond})
	request := httptest.NewRequest(http.MethodGet, "/hooks/updates", nil)
	request.Header.Set("Accept", "text/event-stream")
	done := make(chan struct{})
	go func() { handler.ServeHTTP(httptest.NewRecorder(), request); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream exceeded its maximum duration")
	}
	if metrics := handler.Metrics(); metrics.Active != 0 || metrics.Opened != 1 {
		t.Fatalf("unexpected metrics after duration limit: %+v", metrics)
	}
}

func TestHandlerEmitsHeartbeatsAndTracksActiveStreams(t *testing.T) {
	reader, writer := io.Pipe()
	handler := NewHandler(plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream, Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://events.example.test/stream"}}, nil
	}), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
	}), Config{HeartbeatInterval: 10 * time.Millisecond, ReconnectDelay: time.Second})
	server := httptest.NewServer(handler)
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/hooks/updates", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	lines := bufio.NewReader(response.Body)
	if line, _ := lines.ReadString('\n'); line != "retry: 1000\n" {
		t.Fatalf("unexpected retry prelude %q", line)
	}
	_, _ = lines.ReadString('\n')
	if line, _ := lines.ReadString('\n'); line != ": gateway heartbeat\n" {
		t.Fatalf("unexpected heartbeat %q", line)
	}
	if metrics := handler.Metrics(); metrics.Active != 1 || metrics.Opened != 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
	_ = writer.Close()
}

func TestSSEBoundaryHandlesSplitEventsAndCRLF(t *testing.T) {
	boundary := sseBoundary{complete: true}
	boundary.feed([]byte("data: first\n"))
	if boundary.safe() {
		t.Fatal("a heartbeat would split an unfinished event")
	}
	boundary.feed([]byte("data: second\r"))
	if boundary.safe() {
		t.Fatal("a heartbeat would split a CRLF line ending")
	}
	boundary.feed([]byte("\n\r"))
	if boundary.safe() {
		t.Fatal("a heartbeat would split a CRLF event boundary")
	}
	boundary.feed([]byte("\n"))
	if !boundary.safe() {
		t.Fatal("a complete event should permit heartbeats")
	}
	boundary.feed([]byte("data: next"))
	if boundary.safe() {
		t.Fatal("a partial next event should block heartbeats")
	}
}

func TestFlushCopyDoesNotInsertHeartbeatInsideEvent(t *testing.T) {
	reader, source := io.Pipe()
	response := httptest.NewRecorder()
	done := make(chan error, 1)
	go func() {
		done <- flushCopy(context.Background(), response, reader, 5*time.Millisecond)
	}()
	if _, err := io.WriteString(source, "data: first\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	if _, err := io.WriteString(source, "data: second\n\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(25 * time.Millisecond)
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := response.Body.Bytes(); !bytes.HasPrefix(got, []byte("data: first\ndata: second\n\n: gateway heartbeat\n\n")) {
		t.Fatalf("heartbeat was missing or split an event: %q", got)
	}
}
