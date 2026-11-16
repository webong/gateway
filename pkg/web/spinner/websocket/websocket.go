// Package websocket adapts WebSocket sessions to the protocol-neutral gateway
// planner. The handler owns the socket lifecycle; PHP decides whether a
// connection or message is accepted and which subscriber deliveries happen.
package websocket

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	bridge "github.com/webong/gateway/arranger/integration"
)

const (
	defaultMaxMessageSize = 10 * 1024 * 1024
	defaultPlannerTimeout = 30 * time.Second
	defaultMaxConnections = 1000
	defaultMaxDuration    = 24 * time.Hour
)

type Config struct {
	MaxMessageSize int64
	PlannerTimeout time.Duration
	MaxConnections int
	MaxDuration    time.Duration
	Admission      *Admission
}

// Admission is shared by handlers that serve one listener, including while
// the Planner is deciding whether to accept a connection.
type Admission struct {
	active atomic.Int64
	limit  int64
}

func NewAdmission(limit int) *Admission {
	if limit <= 0 {
		limit = defaultMaxConnections
	}
	return &Admission{limit: int64(limit)}
}

func (a *Admission) acquire() bool {
	for {
		active := a.active.Load()
		if active >= a.limit {
			return false
		}
		if a.active.CompareAndSwap(active, active+1) {
			return true
		}
	}
}

type Handler struct {
	planner   bridge.ProtocolPlanner
	executor  bridge.GatewayExecutor
	config    Config
	admission *Admission
	upgrader  websocket.Upgrader
}

func NewHandler(planner bridge.ProtocolPlanner, executor any, config Config) *Handler {
	if config.MaxMessageSize == 0 {
		config.MaxMessageSize = defaultMaxMessageSize
	}
	if config.PlannerTimeout == 0 {
		config.PlannerTimeout = defaultPlannerTimeout
	}
	if config.MaxDuration <= 0 {
		config.MaxDuration = defaultMaxDuration
	}
	if config.Admission == nil {
		config.Admission = NewAdmission(config.MaxConnections)
	}

	return &Handler{
		planner:   planner,
		executor:  normalizeExecutor(executor),
		config:    config,
		admission: config.Admission,
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
		EventID:      delivery.Attributes["event_id"],
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
	if !h.admission.acquire() {
		writer.Header().Set("Retry-After", "1")
		http.Error(writer, "WebSocket connection limit reached", http.StatusServiceUnavailable)
		return
	}
	defer h.admission.active.Add(-1)
	deadline := time.Now().Add(h.config.MaxDuration)

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
	// Deliver connection events only after Gorilla validates the handshake/origin.
	if connectDecision.Action == bridge.GatewayDeliver && !h.enqueue(connectDecision.Deliveries, nil) {
		_ = writeClose(connection, websocket.CloseTryAgainLater, "gateway delivery queue unavailable")
		return
	}
	connection.SetReadLimit(h.config.MaxMessageSize)
	_ = connection.SetReadDeadline(deadline)
	_ = connection.SetWriteDeadline(deadline)

	sequence := 0
	for {
		messageType, payload, readErr := connection.ReadMessage()
		if readErr != nil {
			_ = connection.Close()
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
			if !h.enqueue(decision.Deliveries, payload) {
				_ = writeClose(connection, websocket.CloseTryAgainLater, "gateway delivery queue unavailable")
				return
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
	decision, err := h.plan(context.Background(), bridge.GatewayEvent{
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
	if err != nil {
		log.Print("WebSocket close-event planning failed")
		return
	}
	if decision.Action == bridge.GatewayDeliver && !h.enqueue(decision.Deliveries, nil) {
		log.Print("WebSocket close-event delivery queue unavailable")
	}
}

// Only message events supply fallback payload bytes. Connection and close
// notifications preserve exactly the payload selected by Planner.
func (h *Handler) enqueue(deliveries []bridge.GatewayDelivery, fallback []byte) bool {
	for _, delivery := range deliveries {
		if len(delivery.Payload) == 0 {
			delivery.Payload = append([]byte(nil), fallback...)
		}
		if !h.executor.EnqueueGateway(delivery) {
			return false
		}
	}
	return true
}

func writeConnectionDecision(writer http.ResponseWriter, decision bridge.GatewayDecision) bool {
	switch decision.Action {
	case bridge.GatewayAccept, bridge.GatewayDeliver:
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
