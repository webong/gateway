// Package webstream proxies Planner-authorized Server-Sent Event sources.
package webstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

type Source interface {
	OpenWebstream(context.Context, bridge.GatewayDelivery) (*http.Response, error)
}

type Handler struct {
	planner bridge.ProtocolPlanner
	source  Source
	config  Config
	active  atomic.Int64
	opened  atomic.Uint64
	failed  atomic.Uint64
}

type Config struct {
	PlannerTimeout    time.Duration
	HeartbeatInterval time.Duration
	ReconnectDelay    time.Duration
}

type Metrics struct {
	Active int64
	Opened uint64
	Failed uint64
}

func NewHandler(planner bridge.ProtocolPlanner, source Source, config Config) *Handler {
	if config.PlannerTimeout == 0 {
		config.PlannerTimeout = 30 * time.Second
	}
	if config.ReconnectDelay == 0 {
		config.ReconnectDelay = 3 * time.Second
	}
	return &Handler{planner: planner, source: source, config: config}
}

func (h *Handler) Metrics() Metrics {
	return Metrics{Active: h.active.Load(), Opened: h.opened.Load(), Failed: h.failed.Load()}
}

func IsRequest(request *http.Request) bool {
	return request != nil && request.Method == http.MethodGet && strings.Contains(strings.ToLower(request.Header.Get("Accept")), "text/event-stream")
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h == nil || h.planner == nil || h.source == nil {
		http.Error(writer, "Webstream gateway is not configured", http.StatusServiceUnavailable)
		return
	}
	if !IsRequest(request) {
		http.Error(writer, "Webstream requires GET with Accept: text/event-stream", http.StatusNotAcceptable)
		return
	}
	sessionID := sessionID(request)
	ctx, cancel := context.WithTimeout(request.Context(), h.config.PlannerTimeout)
	attributes := map[string]string{"remote_addr": request.RemoteAddr}
	if lastEventID := request.Header.Get("Last-Event-ID"); lastEventID != "" {
		attributes["last_event_id"] = lastEventID
	}
	decision, err := h.planner.PlanEvent(ctx, bridge.GatewayEvent{
		ID: sessionID, Protocol: bridge.ProtocolWebstream, Kind: bridge.EventConnect, SessionID: sessionID,
		Host: request.Host, Route: request.URL.Path, Method: request.Method, RawQuery: request.URL.RawQuery,
		Headers: cloneHeaders(request.Header), Attributes: attributes,
	})
	cancel()
	if err != nil || decision.Validate() != nil || decision.Protocol != bridge.ProtocolWebstream {
		h.failed.Add(1)
		http.Error(writer, "Webstream connection planning failed", http.StatusBadGateway)
		return
	}
	switch decision.Action {
	case bridge.GatewayStream:
		response, openErr := h.source.OpenWebstream(request.Context(), *decision.Stream)
		if openErr != nil {
			h.failed.Add(1)
			http.Error(writer, "Webstream source unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			h.failed.Add(1)
			http.Error(writer, "Webstream source rejected connection", http.StatusBadGateway)
			return
		}
		if !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
			h.failed.Add(1)
			http.Error(writer, "Webstream source did not return Server-Sent Events", http.StatusBadGateway)
			return
		}
		copyHeaders(writer.Header(), response.Header)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Del("Content-Length")
		writer.Header().Set("Cache-Control", "no-cache")
		writer.Header().Set("X-Accel-Buffering", "no")
		writer.WriteHeader(response.StatusCode)
		_, _ = writer.Write([]byte("retry: " + strconv.FormatInt(h.config.ReconnectDelay.Milliseconds(), 10) + "\n\n"))
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		h.opened.Add(1)
		h.active.Add(1)
		defer h.active.Add(-1)
		if err := flushCopy(request.Context(), writer, response.Body, h.config.HeartbeatInterval); err != nil && request.Context().Err() == nil {
			h.failed.Add(1)
		}
	case bridge.GatewayRespond:
		copyHeaders(writer.Header(), decision.Headers)
		writer.WriteHeader(decision.StatusCode)
		_, _ = writer.Write(decision.Payload)
	case bridge.GatewayReject, bridge.GatewayClose:
		status := decision.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusForbidden
		}
		http.Error(writer, decision.Message, status)
	default:
		http.Error(writer, "Webstream connection was not authorized", http.StatusForbidden)
	}
}

func flushCopy(ctx context.Context, writer http.ResponseWriter, source io.Reader, heartbeat time.Duration) error {
	flusher, _ := writer.(http.Flusher)
	type readResult struct {
		bytes []byte
		err   error
	}
	reads := make(chan readResult, 1)
	go func() {
		buffer := make([]byte, 32*1024)
		for {
			n, err := source.Read(buffer)
			result := readResult{err: err}
			if n > 0 {
				result.bytes = append([]byte(nil), buffer[:n]...)
			}
			select {
			case reads <- result:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	var ticker *time.Ticker
	if heartbeat > 0 {
		ticker = time.NewTicker(heartbeat)
		defer ticker.Stop()
	}
	var ticks <-chan time.Time
	if ticker != nil {
		ticks = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticks:
			if _, err := writer.Write([]byte(": gateway heartbeat\n\n")); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
		case result := <-reads:
			if len(result.bytes) > 0 {
				if _, err := writer.Write(result.bytes); err != nil {
					return err
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if result.err != nil {
				if result.err == io.EOF {
					return nil
				}
				return result.err
			}
		}
	}
}

func copyHeaders(destination, source http.Header) {
	for name, values := range source {
		if hopByHop(name) {
			continue
		}
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}
func hopByHop(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return false
}
func cloneHeaders(headers http.Header) map[string][]string {
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
func sessionID(request *http.Request) string {
	sum := sha256.Sum256([]byte(request.RemoteAddr + "\n" + request.Host + "\n" + request.URL.RequestURI() + "\n" + time.Now().UTC().Format(time.RFC3339Nano)))
	return "sse-" + hex.EncodeToString(sum[:12])
}
