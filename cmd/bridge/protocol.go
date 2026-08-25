package webrelay

import (
	"fmt"
	"strings"
)

// Protocol identifies the network protocol that produced a gateway event.
type Protocol string

const (
	ProtocolHTTP      Protocol = "http"
	ProtocolWebSocket Protocol = "websocket"
	ProtocolSMTP      Protocol = "smtp"
)

func (p Protocol) Validate() error {
	switch p {
	case ProtocolHTTP, ProtocolWebSocket, ProtocolSMTP:
		return nil
	default:
		return fmt.Errorf("unsupported gateway protocol %q", p)
	}
}

// EventKind describes the unit presented to the PHP control plane. HTTP is
// request-oriented; WebSocket and SMTP will use session-oriented events.
type EventKind string

const (
	EventRequest     EventKind = "request"
	EventConnect     EventKind = "connect"
	EventMessage     EventKind = "message"
	EventClose       EventKind = "close"
	EventTransaction EventKind = "transaction"
)

func (k EventKind) Validate() error {
	switch k {
	case EventRequest, EventConnect, EventMessage, EventClose, EventTransaction:
		return nil
	default:
		return fmt.Errorf("unsupported gateway event kind %q", k)
	}
}

// GatewayEvent is the protocol-neutral event contract for future network
// adapters. Payload is base64 encoded by encoding/json. Route is an HTTP path,
// WebSocket path/channel, or SMTP domain/mailbox depending on Protocol.
type GatewayEvent struct {
	ID         string              `json:"id"`
	Protocol   Protocol            `json:"protocol"`
	Kind       EventKind           `json:"kind"`
	SessionID  string              `json:"session_id,omitempty"`
	Host       string              `json:"host,omitempty"`
	Route      string              `json:"route"`
	Method     string              `json:"method,omitempty"`
	RawQuery   string              `json:"raw_query,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Attributes map[string]string   `json:"attributes,omitempty"`
	Payload    []byte              `json:"payload,omitempty"`
}

func (e GatewayEvent) Validate() error {
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("gateway event ID is required")
	}
	if err := e.Protocol.Validate(); err != nil {
		return err
	}
	if err := e.Kind.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(e.Route) == "" {
		return fmt.Errorf("gateway event route is required")
	}
	if e.Protocol == ProtocolHTTP && e.Kind != EventRequest {
		return fmt.Errorf("HTTP gateway events must use the request kind")
	}
	return nil
}

// GatewayAction is deliberately separate from HTTP PlanAction. SMTP and
// WebSocket sessions need accept/reject/close decisions that are not HTTP
// responses or pass-through requests.
type GatewayAction string

const (
	GatewayAccept  GatewayAction = "accept"
	GatewayReject  GatewayAction = "reject"
	GatewayRespond GatewayAction = "respond"
	GatewayDeliver GatewayAction = "deliver"
	GatewayClose   GatewayAction = "close"
)

// GatewayDelivery is the protocol-neutral destination shape. HTTP Delivery
// remains the stable v1 contract; future adapters can use Target and
// Attributes without pretending SMTP or WebSocket destinations are URLs.
type GatewayDelivery struct {
	Protocol     Protocol            `json:"protocol"`
	Target       string              `json:"target"`
	SubscriberID string              `json:"subscriber_id,omitempty"`
	Headers      map[string][]string `json:"headers,omitempty"`
	Attributes   map[string]string   `json:"attributes,omitempty"`
	Payload      []byte              `json:"payload,omitempty"`
}

func (d GatewayDelivery) Validate() error {
	if err := d.Protocol.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(d.Target) == "" {
		return fmt.Errorf("gateway delivery target is required")
	}
	return nil
}

// GatewayDecision is the future control-plane response for session-oriented
// protocols. HTTP continues to use RoutePlan until its v1 compatibility
// window is intentionally retired.
type GatewayDecision struct {
	Version    string              `json:"version"`
	Protocol   Protocol            `json:"protocol"`
	Action     GatewayAction       `json:"action"`
	StatusCode int                 `json:"status_code,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Message    string              `json:"message,omitempty"`
	Payload    []byte              `json:"payload,omitempty"`
	Deliveries []GatewayDelivery   `json:"deliveries,omitempty"`
	Metadata   map[string]string   `json:"metadata,omitempty"`
}

func (d GatewayDecision) Validate() error {
	if d.Version != ProtocolVersion {
		return fmt.Errorf("unsupported gateway decision version %q", d.Version)
	}
	if err := d.Protocol.Validate(); err != nil {
		return err
	}
	switch d.Action {
	case GatewayAccept, GatewayReject, GatewayRespond, GatewayDeliver, GatewayClose:
	default:
		return fmt.Errorf("unsupported gateway action %q", d.Action)
	}
	if d.Action == GatewayDeliver && len(d.Deliveries) == 0 {
		return fmt.Errorf("deliver decision requires at least one destination")
	}
	for index := range d.Deliveries {
		if err := d.Deliveries[index].Validate(); err != nil {
			return fmt.Errorf("delivery %d: %w", index, err)
		}
	}
	return nil
}

func GatewayEventFromHTTP(ingress IngressRequest) GatewayEvent {
	return GatewayEvent{
		ID:        ingress.DeliveryID,
		Protocol:  protocolOrDefault(ingress.Protocol, ProtocolHTTP),
		Kind:      eventKindOrDefault(ingress.Event, EventRequest),
		SessionID: ingress.SessionID,
		Host:      ingress.Host,
		Route:     ingress.Path,
		Method:    ingress.Method,
		RawQuery:  ingress.RawQuery,
		Headers:   cloneStringHeaders(ingress.Headers),
		Payload:   append([]byte(nil), ingress.Body...),
	}
}

func protocolOrDefault(protocol Protocol, fallback Protocol) Protocol {
	if protocol == "" {
		return fallback
	}
	return protocol
}

func eventKindOrDefault(kind EventKind, fallback EventKind) EventKind {
	if kind == "" {
		return fallback
	}
	return kind
}

func cloneStringHeaders(headers map[string][]string) map[string][]string {
	if headers == nil {
		return nil
	}
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
