// Package webstream proxies Planner-authorized Server-Sent Event sources.
package webstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
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
	planner   bridge.ProtocolPlanner
	source    Source
	config    Config
	admission *Admission
	active    atomic.Int64
	opened    atomic.Uint64
	failed    atomic.Uint64
}

type Config struct {
	PlannerTimeout    time.Duration
	HeartbeatInterval time.Duration
	ReconnectDelay    time.Duration
	MaxConnections    int
	MaxDuration       time.Duration
	Admission         *Admission
	WriteTimeout      time.Duration
	MaxBodySize       int64
	BodyReadTimeout   time.Duration
}

// Admission limits all streams sharing it, including streams still being planned.
// A Caddy handler shares one admission across its per-request adapters.
type Admission struct {
	active atomic.Int64
	limit  int64
}

func NewAdmission(limit int) *Admission {
	if limit <= 0 {
		limit = 1000
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

type Metrics struct {
	Active int64
	Opened uint64
	Failed uint64
}

func NewHandler(planner bridge.ProtocolPlanner, source Source, config Config) *Handler {
	if config.MaxBodySize <= 0 {
		config.MaxBodySize = 10 * 1024 * 1024
	}
	if config.BodyReadTimeout <= 0 {
		config.BodyReadTimeout = 15 * time.Second
	}
	if config.PlannerTimeout == 0 {
		config.PlannerTimeout = 30 * time.Second
	}
	if config.ReconnectDelay == 0 {
		config.ReconnectDelay = 3 * time.Second
	}
	if config.MaxDuration <= 0 {
		config.MaxDuration = 24 * time.Hour
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = 30 * time.Second
	}
	if config.Admission == nil {
		config.Admission = NewAdmission(config.MaxConnections)
	}
	return &Handler{planner: planner, source: source, config: config, admission: config.Admission}
}

func (h *Handler) Metrics() Metrics {
	return Metrics{Active: h.active.Load(), Opened: h.opened.Load(), Failed: h.failed.Load()}
}

func IsRequest(request *http.Request) bool {
	return request != nil && (request.Method == http.MethodGet || request.Method == http.MethodPost) && strings.Contains(strings.ToLower(request.Header.Get("Accept")), "text/event-stream")
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if h == nil || h.planner == nil || h.source == nil {
		http.Error(writer, "Webstream gateway is not configured", http.StatusServiceUnavailable)
		return
	}
	if !IsRequest(request) {
		http.Error(writer, "Webstream requires GET or POST with Accept: text/event-stream", http.StatusNotAcceptable)
		return
	}
	if !h.admission.acquire() {
		h.failed.Add(1)
		writer.Header().Set("Retry-After", "1")
		http.Error(writer, "Webstream connection limit reached", http.StatusServiceUnavailable)
		return
	}
	defer h.admission.active.Add(-1)
	var payload []byte
	if request.Method == http.MethodPost {
		if encoding := request.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
			http.Error(writer, "Encoded Webstream request bodies are not supported", http.StatusUnsupportedMediaType)
			return
		}
		controller := http.NewResponseController(writer)
		_ = controller.SetReadDeadline(time.Now().Add(h.config.BodyReadTimeout))
		body := http.MaxBytesReader(writer, request.Body, h.config.MaxBodySize)
		var err error
		payload, err = io.ReadAll(body)
		_ = body.Close()
		_ = controller.SetReadDeadline(time.Time{})
		if err != nil {
			h.failed.Add(1)
			var tooLarge *http.MaxBytesError
			status := http.StatusBadRequest
			if errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
				status = http.StatusRequestTimeout
			}
			http.Error(writer, "Webstream request body rejected", status)
			return
		}
	}
	streamContext, streamCancel := context.WithTimeout(request.Context(), h.config.MaxDuration)
	defer streamCancel()
	sessionID := sessionID(request)
	ctx, cancel := context.WithTimeout(streamContext, h.config.PlannerTimeout)
	attributes := map[string]string{"remote_addr": request.RemoteAddr}
	if lastEventID := request.Header.Get("Last-Event-ID"); lastEventID != "" {
		attributes["last_event_id"] = lastEventID
	}
	decision, err := h.planner.PlanEvent(ctx, bridge.GatewayEvent{
		ID: sessionID, Protocol: bridge.ProtocolWebstream, Kind: bridge.EventConnect, SessionID: sessionID,
		Host: request.Host, Route: request.URL.Path, Method: request.Method, RawQuery: request.URL.RawQuery,
		Headers: cloneHeaders(request.Header), Attributes: attributes, Payload: payload,
	})
	cancel()
	if err != nil || decision.Validate() != nil || decision.Protocol != bridge.ProtocolWebstream {
		h.failed.Add(1)
		http.Error(writer, "Webstream connection planning failed", http.StatusBadGateway)
		return
	}
	switch decision.Action {
	case bridge.GatewayStream:
		method := decision.Stream.Attributes["method"]
		if method == "" {
			method = http.MethodGet
		}
		if method != request.Method || int64(len(decision.Stream.Payload)) > h.config.MaxBodySize {
			h.failed.Add(1)
			http.Error(writer, "Invalid Webstream source request", http.StatusBadGateway)
			return
		}
		// Replace the server's whole-response deadline with a per-write limit.
		controller := http.NewResponseController(writer)
		_ = controller.SetWriteDeadline(time.Time{})
		defer controller.SetWriteDeadline(time.Time{})
		writer = &streamWriter{ResponseWriter: writer, controller: controller, timeout: h.config.WriteTimeout}
		response, openErr := h.source.OpenWebstream(streamContext, *decision.Stream)
		if openErr != nil {
			h.failed.Add(1)
			http.Error(writer, "Webstream source unavailable", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		if response.StatusCode == http.StatusNoContent {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if response.StatusCode != http.StatusOK {
			h.failed.Add(1)
			http.Error(writer, "Webstream source rejected connection", http.StatusBadGateway)
			return
		}
		mediaType, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		if mediaErr != nil || !strings.EqualFold(mediaType, "text/event-stream") {
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
		if request.Method == http.MethodGet {
			if _, err := writer.Write([]byte("retry: " + strconv.FormatInt(h.config.ReconnectDelay.Milliseconds(), 10) + "\n\n")); err != nil {
				h.failed.Add(1)
				return
			}
		}
		if err := http.NewResponseController(writer).Flush(); err != nil {
			h.failed.Add(1)
			return
		}
		h.opened.Add(1)
		h.active.Add(1)
		defer h.active.Add(-1)
		if err := flushCopy(streamContext, writer, response.Body, h.config.HeartbeatInterval); err != nil && streamContext.Err() == nil {
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	controller := http.NewResponseController(writer)
	// An empty stream (or the GET retry prelude) is an event boundary.
	// Heartbeats must not be inserted into a source's incomplete event.
	boundary := sseBoundary{complete: true}
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
			if !boundary.safe() {
				continue
			}
			if _, err := writer.Write([]byte(": gateway heartbeat\n\n")); err != nil {
				return err
			}
			if err := controller.Flush(); err != nil {
				return err
			}
		case result := <-reads:
			if len(result.bytes) > 0 {
				if _, err := writer.Write(result.bytes); err != nil {
					return err
				}
				boundary.feed(result.bytes)
				if err := controller.Flush(); err != nil {
					return err
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

type streamWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
	timeout    time.Duration
}

func (w *streamWriter) Write(p []byte) (int, error) {
	_ = w.controller.SetWriteDeadline(time.Now().Add(w.timeout))
	return w.ResponseWriter.Write(p)
}
func (w *streamWriter) FlushError() error {
	_ = w.controller.SetWriteDeadline(time.Now().Add(w.timeout))
	err := w.controller.Flush()
	_ = w.controller.SetWriteDeadline(time.Time{})
	return err
}

// sseBoundary tracks whether the byte stream ends after an empty SSE line.
// CRLF is one line ending even when its two bytes arrive in separate reads.
type sseBoundary struct {
	lineHasData bool
	complete    bool
	lastCR      bool
}

func (b *sseBoundary) feed(chunk []byte) {
	for _, value := range chunk {
		if b.lastCR {
			b.lastCR = false
			if value == '\n' {
				continue
			}
		}
		switch value {
		case '\r', '\n':
			b.complete = !b.lineHasData
			b.lineHasData = false
			b.lastCR = value == '\r'
		default:
			b.lineHasData = true
			b.complete = false
		}
	}
}

func (b sseBoundary) safe() bool {
	return b.complete && !b.lastCR
}

func copyHeaders(destination, source http.Header) {
	blocked := map[string]bool{}
	for _, connection := range source.Values("Connection") {
		for _, field := range strings.Split(connection, ",") {
			if field = strings.ToLower(strings.TrimSpace(field)); field != "" {
				blocked[field] = true
			}
		}
	}
	for name, values := range source {
		if hopByHop(name) || blocked[strings.ToLower(name)] {
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
