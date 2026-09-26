// Package websocket adapts WebSocket sessions to the protocol-neutral gateway
// planner. The handler owns the socket lifecycle; PHP decides whether a
// connection or message is accepted and which subscriber deliveries happen.
package websocket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

const (
	defaultMaxMessageSize = 10 * 1024 * 1024
	defaultPlannerTimeout = 30 * time.Second
)

type Config struct {
	MaxMessageSize int64
	PlannerTimeout time.Duration
}

type Handler struct {
	planner  bridge.ProtocolPlanner
	executor bridge.GatewayExecutor
	config   Config
	upgrader websocket.Upgrader
}

func NewHandler(planner bridge.ProtocolPlanner, executor any, config Config) *Handler {
	if config.MaxMessageSize == 0 {
		config.MaxMessageSize = defaultMaxMessageSize
	}
	if config.PlannerTimeout == 0 {
		config.PlannerTimeout = defaultPlannerTimeout
	}

	return &Handler{
		planner:  planner,
		executor: normalizeExecutor(executor),
		config:   config,
		upgrader: websocket.Upgrader{
			// Empty Origin is valid for non-browser clients. Browser clients must
			// still use the same host unless the host application supplies a
			// dedicated CORS/origin policy before this handler.
			CheckOrigin: func(request *http.Request) bool {
				origin := request.Header.Get("Origin")
				return origin == "" || sameOrigin(request, origin)
			},
		},
	}
}

func normalizeExecutor(value any) bridge.GatewayExecutor {
	if executor, ok := value.(bridge.GatewayExecutor); ok {
		return executor
	}
	if executor, ok := value.(bridge.Executor); ok {
		return transportExecutor{executor: executor}
	}
	return nil
}

type transportExecutor struct {
	executor bridge.Executor
}

func (e transportExecutor) EnqueueGateway(delivery bridge.GatewayDelivery) bool {
	if e.executor == nil || delivery.Protocol != bridge.ProtocolHTTP || strings.TrimSpace(delivery.Target) == "" {
		return false
	}

	method := delivery.Attributes["method"]
	if method == "" {
		method = http.MethodPost
	}

	return e.executor.Enqueue(bridge.Delivery{
		URL:          delivery.Target,
		Method:       method,
		RawQuery:     delivery.Attributes["raw_query"],
		Headers:      delivery.Headers,
		Body:         append([]byte(nil), delivery.Payload...),
		SubscriberID: delivery.SubscriberID,
	})
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h == nil || h.planner == nil || h.executor == nil {
		http.Error(writer, "WebSocket gateway is not configured", http.StatusServiceUnavailable)
		return
	}
	if !IsUpgrade(request) {
		http.Error(writer, "WebSocket upgrade required", http.StatusUpgradeRequired)
		return
	}

	sessionID := sessionID(request)
	connectDecision, err := h.plan(request.Context(), bridge.GatewayEvent{
		ID:        sessionID + ":connect",
		Protocol:  bridge.ProtocolWebSocket,
		Kind:      bridge.EventConnect,
		SessionID: sessionID,
		Host:      request.Host,
		Route:     request.URL.Path,
		Method:    request.Method,
		RawQuery:  request.URL.RawQuery,
		Headers:   cloneHeaders(request.Header),
		Attributes: map[string]string{
			"remote_addr": request.RemoteAddr,
		},
	})
	if err != nil {
		http.Error(writer, "WebSocket connection planning failed", http.StatusBadGateway)
		return
	}
	if !writeConnectionDecision(writer, connectDecision) {
		return
	}

	connection, err := h.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(h.config.MaxMessageSize)

	sequence := 0
	for {
		messageType, payload, readErr := connection.ReadMessage()
		if readErr != nil {
			h.planClose(request, sessionID, sequence, readErr)
			return
		}
		sequence++

		decision, planErr := h.plan(request.Context(), bridge.GatewayEvent{
			ID:        messageID(sessionID, sequence, payload),
			Protocol:  bridge.ProtocolWebSocket,
			Kind:      bridge.EventMessage,
			SessionID: sessionID,
			Host:      request.Host,
			Route:     request.URL.Path,
			Method:    request.Method,
			RawQuery:  request.URL.RawQuery,
			Headers:   cloneHeaders(request.Header),
			Attributes: map[string]string{
				"message_type": messageTypeName(messageType),
				"sequence":     fmt.Sprintf("%d", sequence),
			},
			Payload: payload,
		})
		if planErr != nil {
			_ = writeClose(connection, websocket.CloseInternalServerErr, "gateway planning failed")
			return
		}

		switch decision.Action {
		case bridge.GatewayAccept:
			continue
		case bridge.GatewayRespond:
			if err := connection.WriteMessage(responseMessageType(decision), decision.Payload); err != nil {
				return
			}
		case bridge.GatewayDeliver:
			for index := range decision.Deliveries {
				delivery := decision.Deliveries[index]
				if len(delivery.Payload) == 0 {
					delivery.Payload = append([]byte(nil), payload...)
				}
				if !h.executor.EnqueueGateway(delivery) {
					_ = writeClose(connection, websocket.CloseTryAgainLater, "gateway delivery queue unavailable")
					return
				}
			}
		case bridge.GatewayReject, bridge.GatewayClose:
			_ = writeClose(connection, websocket.ClosePolicyViolation, decision.Message)
			return
		default:
			_ = writeClose(connection, websocket.CloseInternalServerErr, "unsupported gateway decision")
			return
		}
	}
}

func (h *Handler) plan(parent context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
	ctx, cancel := context.WithTimeout(parent, h.config.PlannerTimeout)
	defer cancel()

	decision, err := h.planner.PlanEvent(ctx, event)
	if err != nil {
		return bridge.GatewayDecision{}, err
	}
	if err := decision.Validate(); err != nil {
		return bridge.GatewayDecision{}, err
	}
	if decision.Protocol != bridge.ProtocolWebSocket {
		return bridge.GatewayDecision{}, fmt.Errorf("gateway decision protocol is %q, expected websocket", decision.Protocol)
	}

	return decision, nil
}

func (h *Handler) planClose(request *http.Request, sessionID string, sequence int, cause error) {
	_, _ = h.plan(context.Background(), bridge.GatewayEvent{
		ID:        fmt.Sprintf("%s:close:%d", sessionID, sequence),
		Protocol:  bridge.ProtocolWebSocket,
		Kind:      bridge.EventClose,
		SessionID: sessionID,
		Host:      request.Host,
		Route:     request.URL.Path,
		Method:    request.Method,
		RawQuery:  request.URL.RawQuery,
		Attributes: map[string]string{
			"close_reason": cause.Error(),
			"sequence":     fmt.Sprintf("%d", sequence),
		},
	})
}

func writeConnectionDecision(writer http.ResponseWriter, decision bridge.GatewayDecision) bool {
	switch decision.Action {
	case bridge.GatewayAccept:
		return true
	case bridge.GatewayRespond:
		status := decision.StatusCode
		if status < 100 || status > 599 {
			status = http.StatusForbidden
		}
		for name, values := range decision.Headers {
			for _, value := range values {
				writer.Header().Add(name, value)
			}
		}
		writer.WriteHeader(status)
		_, _ = writer.Write(decision.Payload)
	case bridge.GatewayReject, bridge.GatewayClose:
		status := decision.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusForbidden
		}
		http.Error(writer, decision.Message, status)
	default:
		http.Error(writer, "unsupported WebSocket connection decision", http.StatusBadGateway)
	}

	return false
}

func writeClose(connection *websocket.Conn, code int, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "gateway closed the connection"
	}
	return connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason),
		time.Now().Add(time.Second),
	)
}

func responseMessageType(decision bridge.GatewayDecision) int {
	if decision.Metadata != nil && decision.Metadata["message_type"] == "text" {
		return websocket.TextMessage
	}
	return websocket.BinaryMessage
}

func messageTypeName(messageType int) string {
	if messageType == websocket.TextMessage {
		return "text"
	}
	if messageType == websocket.BinaryMessage {
		return "binary"
	}
	return "control"
}

func sessionID(request *http.Request) string {
	hash := sha256.Sum256([]byte(request.RemoteAddr + "\n" + request.Host + "\n" + request.URL.RequestURI() + "\n" + time.Now().UTC().Format(time.RFC3339Nano)))
	return "ws-" + hex.EncodeToString(hash[:12])
}

func messageID(sessionID string, sequence int, payload []byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(fmt.Sprintf("%s\n%d\n", sessionID, sequence)))
	_, _ = hash.Write(payload)
	return "ws-" + hex.EncodeToString(hash.Sum(nil)[:16])
}

func sameOrigin(request *http.Request, origin string) bool {
	originRequest, err := http.NewRequest(http.MethodGet, origin, nil)
	if err != nil {
		return false
	}
	return strings.EqualFold(originRequest.Host, request.Host)
}

func cloneHeaders(headers http.Header) map[string][]string {
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func IsUpgrade(request *http.Request) bool {
	if request == nil {
		return false
	}
	for _, value := range request.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") && strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
				return true
			}
		}
	}
	return false
}
