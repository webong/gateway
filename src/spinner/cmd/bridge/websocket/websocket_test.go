package websocket

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

type plannerFunc func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error)

func (f plannerFunc) PlanEvent(ctx context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
	return f(ctx, event)
}

type recordingExecutor struct {
	deliveries chan bridge.GatewayDelivery
}

func (e *recordingExecutor) EnqueueGateway(delivery bridge.GatewayDelivery) bool {
	e.deliveries <- delivery
	return true
}

func TestHandlerPlansWebSocketSessionAndQueuesMessages(t *testing.T) {
	events := make(chan bridge.GatewayEvent, 4)
	executor := &recordingExecutor{deliveries: make(chan bridge.GatewayDelivery, 1)}
	handler := NewHandler(plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		events <- event
		switch event.Kind {
		case bridge.EventConnect, bridge.EventClose:
			return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebSocket, Action: bridge.GatewayAccept}, nil
		case bridge.EventMessage:
			return bridge.GatewayDecision{
				Version:  bridge.ProtocolVersion,
				Protocol: bridge.ProtocolWebSocket,
				Action:   bridge.GatewayDeliver,
				Deliveries: []bridge.GatewayDelivery{{
					Protocol: bridge.ProtocolHTTP,
					Target:   "https://subscriber.example.test/websocket",
				}},
			}, nil
		default:
			return bridge.GatewayDecision{}, nil
		}
	}), executor, Config{PlannerTimeout: time.Second})

	server := httptest.NewServer(handler)
	defer server.Close()
	wsURL := "ws" + server.URL[len("http"):]
	client, _, err := websocket.DefaultDialer.Dial(wsURL+"/channels/updates", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := client.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}

	select {
	case delivery := <-executor.deliveries:
		if delivery.Protocol != bridge.ProtocolHTTP || string(delivery.Payload) != "hello" {
			t.Fatalf("unexpected queued WebSocket delivery: %+v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("WebSocket message was not queued")
	}

	if err := client.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	seenConnect, seenMessage := false, false
	deadline := time.After(time.Second)
	for !(seenConnect && seenMessage) {
		select {
		case event := <-events:
			seenConnect = seenConnect || event.Kind == bridge.EventConnect
			seenMessage = seenMessage || event.Kind == bridge.EventMessage
		case <-deadline:
			t.Fatalf("missing WebSocket events: connect=%v message=%v", seenConnect, seenMessage)
		}
	}
}

func TestHandlerRejectsNonWebSocketRequests(t *testing.T) {
	handler := NewHandler(plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		t.Fatal("planner should not run without an upgrade")
		return bridge.GatewayDecision{}, nil
	}), &recordingExecutor{deliveries: make(chan bridge.GatewayDelivery, 1)}, Config{})

	record := httptest.NewRecorder()
	handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/channels/updates", nil))
	if record.Code != http.StatusUpgradeRequired {
		t.Fatalf("expected upgrade required response, got %d", record.Code)
	}
}

func TestHandlerLimitsSessionsBeforePlanningAndReleasesSlots(t *testing.T) {
	var plans atomic.Int64
	handler := NewHandler(plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		if event.Kind == bridge.EventConnect {
			plans.Add(1)
		}
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebSocket, Action: bridge.GatewayAccept}, nil
	}), &recordingExecutor{deliveries: make(chan bridge.GatewayDelivery, 1)}, Config{MaxConnections: 1, MaxDuration: time.Second})
	server := httptest.NewServer(handler)
	defer server.Close()
	url := "ws" + server.URL[len("http"):]
	first, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, response, err := websocket.DefaultDialer.Dial(url, nil)
	if second != nil {
		second.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Retry-After") != "1" || plans.Load() != 1 {
		t.Fatalf("expected admission rejection before planning: response=%v plans=%d err=%v", response, plans.Load(), err)
	}
	first.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		third, response, err := websocket.DefaultDialer.Dial(url, nil)
		if err == nil {
			third.Close()
			if plans.Load() != 2 {
				t.Fatalf("expected reopened slot to be planned, got %d plans", plans.Load())
			}
			return
		}
		if response == nil || response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("unexpected reconnect failure: response=%v err=%v", response, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("WebSocket admission slot was not released")
}

func TestHandlerClosesIdleSessionAtMaximumDuration(t *testing.T) {
	handler := NewHandler(plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebSocket, Action: bridge.GatewayAccept}, nil
	}), &recordingExecutor{deliveries: make(chan bridge.GatewayDelivery, 1)}, Config{MaxDuration: 30 * time.Millisecond})
	server := httptest.NewServer(handler)
	defer server.Close()
	url := "ws" + server.URL[len("http"):]
	client, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	start := time.Now()
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := client.ReadMessage(); err == nil {
		t.Fatal("idle WebSocket survived its maximum duration")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("the client timed out before Gateway closed the idle session: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("Gateway took %s to close a 30ms WebSocket session", elapsed)
	}
}
