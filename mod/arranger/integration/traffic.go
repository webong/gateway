package integration

import (
	"fmt"
	"strings"
)

// Protocol identifies the network protocol that produced a gateway event.
type Protocol string

const (
	ProtocolHTTP      Protocol = "http"
	ProtocolWebSocket Protocol = "websocket"
	ProtocolWebstream Protocol = "webstream"
	ProtocolSMTP      Protocol = "smtp"
	ProtocolPOP3      Protocol = "pop3"
	ProtocolIMAP      Protocol = "imap"
	ProtocolDNS       Protocol = "dns"
)

func (p Protocol) Validate() error {
	if !validAdapterName(string(p)) {
		return fmt.Errorf("invalid gateway protocol %q", p)
	}
	return nil
}

// EventKind describes the unit presented to the PHP control plane. HTTP is
// request-oriented; WebSocket and SMTP use session-oriented events.
type EventKind string

const (
	EventRequest      EventKind = "request"
	EventConnect      EventKind = "connect"
	EventMessage      EventKind = "message"
	EventClose        EventKind = "close"
	EventTransaction  EventKind = "transaction"
	EventRecipient    EventKind = "recipient"
	EventAuthenticate EventKind = "authenticate"
	EventQuery        EventKind = "query"
)

func (k EventKind) Validate() error {
	if !validAdapterName(string(k)) {
		return fmt.Errorf("invalid gateway event kind %q", k)
	}
	return nil
}

// GatewayEvent is the protocol-neutral event contract for session-oriented
// network adapters. Payload is base64 encoded by encoding/json. Route is an
// HTTP path, WebSocket path/channel, or SMTP domain/mailbox depending on
// Protocol.
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
	Runtime    RuntimeCapabilities `json:"runtime,omitempty"`
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
	return nil
}

// GatewayAction is deliberately separate from HTTP PlanAction. Session-based
// Extensions need accept/reject/close decisions that are not HTTP responses or
// pass-through requests.
type GatewayAction string

const (
	GatewayAccept     GatewayAction = "accept"
	GatewayReject     GatewayAction = "reject"
	GatewayRespond    GatewayAction = "respond"
	GatewayDeliver    GatewayAction = "deliver"
	GatewaySubmit     GatewayAction = "submit"
	GatewayQuarantine GatewayAction = "quarantine"
	GatewayClose      GatewayAction = "close"
	GatewayStream     GatewayAction = "stream"
	GatewayAutomate   GatewayAction = "automate"
)

// GatewayDelivery is the protocol-neutral destination shape. HTTP Delivery
// remains the stable v1 contract; future adapters can use Target and
// Attributes without pretending SMTP or WebSocket destinations are URLs.
type GatewayDelivery struct {
	Protocol     Protocol            `json:"protocol"`
	Adapter      string              `json:"adapter,omitempty"`
	Target       string              `json:"target"`
	SubscriberID string              `json:"subscriber_id,omitempty"`
	Headers      map[string][]string `json:"headers,omitempty"`
	Attributes   map[string]string   `json:"attributes,omitempty"`
	Payload      []byte              `json:"payload,omitempty"`
	Batch        []GatewayDelivery   `json:"batch,omitempty"`
	Transport    *HTTPTransport      `json:"transport,omitempty"`
}

func (d GatewayDelivery) Validate() error {
	if err := d.Protocol.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(d.Target) == "" {
		return fmt.Errorf("gateway delivery target is required")
	}
	if d.Adapter != "" && !validAdapterName(d.Adapter) {
		return fmt.Errorf("invalid gateway delivery adapter %q", d.Adapter)
	}
	if d.Adapter == "gateway.fanout" {
		if len(d.Batch) < 2 || len(d.Batch) > 100 || len(d.Payload) == 0 {
			return fmt.Errorf("gateway fanout requires a bounded batch and shared payload")
		}
		for index, child := range d.Batch {
			if child.Adapter == "gateway.fanout" || len(child.Batch) != 0 || len(child.Payload) != 0 {
				return fmt.Errorf("gateway fanout child %d is nested or duplicates payload", index)
			}
			if err := child.Validate(); err != nil {
				return fmt.Errorf("gateway fanout child %d: %w", index, err)
			}
		}
	} else if len(d.Batch) != 0 {
		return fmt.Errorf("gateway batch is reserved for fanout")
	}
	if d.Transport != nil {
		if err := d.Transport.Validate(); err != nil {
			return fmt.Errorf("transport: %w", err)
		}
	}
	return nil
}

func (d GatewayDelivery) AdapterName() string {
	if d.Adapter != "" {
		return d.Adapter
	}
	return string(d.Protocol)
}

func validAdapterName(name string) bool {
	if len(name) == 0 || len(name) > 64 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, character := range name[1:] {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

// ValidAdapterName reports whether name is safe to use as a resolved runtime
// adapter identifier across the Planner-to-Spinner contract.
func ValidAdapterName(name string) bool {
	return validAdapterName(name)
}

// GatewayDecision is the control-plane response for session-oriented
// protocols. HTTP continues to use RoutePlan for compatibility.
type GatewayDecision struct {
	Version    string              `json:"version"`
	Protocol   Protocol            `json:"protocol"`
	Action     GatewayAction       `json:"action"`
	StatusCode int                 `json:"status_code,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Message    string              `json:"message,omitempty"`
	Payload    []byte              `json:"payload,omitempty"`
	Reply      *GatewayDelivery    `json:"reply,omitempty"`
	Deliveries []GatewayDelivery   `json:"deliveries,omitempty"`
	Stream     *GatewayDelivery    `json:"stream,omitempty"`
	Automation *Automation         `json:"automation,omitempty"`
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
	case GatewayAccept, GatewayReject, GatewayRespond, GatewayDeliver, GatewaySubmit, GatewayQuarantine, GatewayClose, GatewayStream, GatewayAutomate:
	default:
		return fmt.Errorf("unsupported gateway action %q", d.Action)
	}
	if d.Action == GatewayDeliver && d.Reply == nil && len(d.Deliveries) == 0 {
		return fmt.Errorf("deliver decision requires a reply or destination")
	}
	if d.Action == GatewaySubmit && (len(d.Deliveries) != 1 || d.Reply != nil || d.Stream != nil) {
		return fmt.Errorf("submit decisions require one delivery")
	}
	if d.Action == GatewayQuarantine && (len(d.Deliveries) != 0 || d.Reply != nil || d.Stream != nil) {
		return fmt.Errorf("quarantine decisions cannot include deliveries")
	}
	if d.Action == GatewayStream {
		if d.Stream == nil || d.Reply != nil || len(d.Deliveries) != 0 {
			return fmt.Errorf("stream decisions require one stream source")
		}
	}
	if d.Action == GatewayAutomate {
		if d.Automation == nil || d.Reply != nil || d.Stream != nil {
			return fmt.Errorf("automate decisions require automation and cannot include reply or stream destinations")
		}
		if err := d.Automation.Validate(); err != nil {
			return err
		}
	} else if d.Automation != nil {
		return fmt.Errorf("automation is only valid for automate decisions")
	}
	if d.Reply != nil {
		if err := d.Reply.Validate(); err != nil {
			return fmt.Errorf("reply: %w", err)
		}
	}
	if d.Stream != nil {
		if err := d.Stream.Validate(); err != nil {
			return fmt.Errorf("stream: %w", err)
		}
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
