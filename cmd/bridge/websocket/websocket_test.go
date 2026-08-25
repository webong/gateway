package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	bridge "github.com/webong/gateway/cmd/bridge"
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
