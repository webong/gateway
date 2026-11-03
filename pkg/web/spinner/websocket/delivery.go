package websocket

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	gorillawebsocket "github.com/gorilla/websocket"
	protocol "github.com/webong/gateway/arranger/integration"
)

const defaultHandshakeTimeout = 15 * time.Second

// DeliveryAdapter sends one queued delivery as one frame over a short-lived
// outbound WebSocket connection. HTTP request headers are never copied into
// the WebSocket handshake; named Planner transport profiles supply credentials.
type DeliveryAdapter struct {
	privateNetworkCeilings []netip.Prefix
}

func NewDeliveryAdapter(privateNetworkCIDRs []string) (*DeliveryAdapter, error) {
	ceilings := make([]netip.Prefix, 0, len(privateNetworkCIDRs))
	for _, raw := range privateNetworkCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
			return nil, fmt.Errorf("invalid WebSocket outbound private-network CIDR %q", raw)
		}
		ceilings = append(ceilings, prefix)
	}
	return &DeliveryAdapter{privateNetworkCeilings: ceilings}, nil
}

func (adapter *DeliveryAdapter) DeliverGateway(ctx context.Context, delivery protocol.GatewayDelivery) error {
	if adapter == nil || delivery.Protocol != protocol.ProtocolWebSocket {
		return fmt.Errorf("%w: WebSocket adapter received an incompatible delivery", protocol.ErrPermanentDelivery)
	}
	if err := delivery.Validate(); err != nil {
		return fmt.Errorf("%w: invalid WebSocket delivery: %v", protocol.ErrPermanentDelivery, err)
	}
	target, err := url.Parse(delivery.Target)
	if err != nil || target.Host == "" || target.User != nil || target.Fragment != "" || (target.Scheme != "ws" && target.Scheme != "wss") {
		return fmt.Errorf("%w: WebSocket delivery target is invalid", protocol.ErrPermanentDelivery)
	}
	if delivery.Transport != nil {
		if timeout := durationSeconds(delivery.Transport.RequestTimeoutSeconds); timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}

	dialer, err := adapter.dialer(delivery.Transport)
	if err != nil {
		return fmt.Errorf("%w: WebSocket transport profile is invalid: %v", protocol.ErrPermanentDelivery, err)
	}
	connection, response, err := dialer.DialContext(ctx, delivery.Target, adapter.handshakeHeaders(delivery))
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if response != nil && response.StatusCode >= 300 && response.StatusCode < 500 {
			return fmt.Errorf("%w: WebSocket target rejected handshake with status %d", protocol.ErrPermanentDelivery, response.StatusCode)
		}
		return fmt.Errorf("connect WebSocket target: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetWriteDeadline(deadline)
	}
	if err := connection.WriteMessage(frameType(delivery), delivery.Payload); err != nil {
		return fmt.Errorf("%w: WebSocket frame write failed: %v", protocol.ErrUncertainDelivery, err)
	}
	_ = connection.WriteControl(gorillawebsocket.CloseMessage,
		gorillawebsocket.FormatCloseMessage(gorillawebsocket.CloseNormalClosure, "delivery complete"),
		time.Now().Add(time.Second))
	return nil
}

func (adapter *DeliveryAdapter) dialer(transport *protocol.HTTPTransport) (*gorillawebsocket.Dialer, error) {
	dialer := &gorillawebsocket.Dialer{
		HandshakeTimeout: defaultHandshakeTimeout,
		NetDialContext:   adapter.safeDialContext(0),
		Proxy:            func(*http.Request) (*url.URL, error) { return nil, nil },
	}
	if transport == nil {
		return dialer, nil
	}
	if err := transport.Validate(); err != nil {
		return nil, err
	}
	if transport.Proxy != nil && (transport.Proxy.HTTP != "" || transport.Proxy.HTTPS != "") {
		return nil, fmt.Errorf("WebSocket delivery cannot use a proxy profile because the proxy would bypass destination network checks")
	}
	if timeout := durationSeconds(transport.ConnectTimeoutSeconds); timeout > 0 {
		dialer.HandshakeTimeout = timeout
	}
	return dialer, nil
}

func (adapter *DeliveryAdapter) safeDialContext(timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		dialer := &net.Dialer{Timeout: timeout}
		var lastErr error
		for _, candidate := range addresses {
			candidate = candidate.Unmap()
			if !adapter.addressAllowed(candidate) {
				continue
			}
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
			if dialErr == nil {
				return connection, nil
			}
			lastErr = dialErr
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("WebSocket target resolves only to blocked addresses")
	}
}

func (adapter *DeliveryAdapter) addressAllowed(address netip.Addr) bool {
	if address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLoopback() && !address.IsLinkLocalUnicast() && !address.IsUnspecified() {
		return true
	}
	for _, prefix := range adapter.privateNetworkCeilings {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func (adapter *DeliveryAdapter) handshakeHeaders(delivery protocol.GatewayDelivery) http.Header {
	headers := make(http.Header)
	if delivery.Transport != nil {
		for name, values := range delivery.Transport.StreamHeaders {
			for _, value := range values {
				headers.Add(name, value)
			}
		}
	}
	if eventID := delivery.Attributes["event_id"]; eventID != "" {
		headers.Set("X-Gateway-Event-ID", eventID)
	}
	return headers
}

func frameType(delivery protocol.GatewayDelivery) int {
	if contentType := delivery.Attributes["content_type"]; contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err == nil {
			if strings.HasPrefix(mediaType, "text/") || strings.Contains(mediaType, "json") || strings.Contains(mediaType, "xml") || strings.Contains(mediaType, "javascript") || mediaType == "application/x-www-form-urlencoded" {
				if utf8.Valid(delivery.Payload) {
					return gorillawebsocket.TextMessage
				}
				return gorillawebsocket.BinaryMessage
			}
			return gorillawebsocket.BinaryMessage
		}
	}
	if utf8.Valid(delivery.Payload) {
		return gorillawebsocket.TextMessage
	}
	return gorillawebsocket.BinaryMessage
}

func durationSeconds(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

var _ protocol.GatewayDeliveryAdapter = (*DeliveryAdapter)(nil)
