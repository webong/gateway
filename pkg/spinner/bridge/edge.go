package gateway

// Edge is the Go-side HTTP adapter mounted in RoadRunner's HTTP pipeline.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/webong/gateway/arranger/automation"
)

// Edge is the Go-side HTTP adapter mounted in RoadRunner's HTTP pipeline. It
// captures the request, asks PHP for a plan, and executes only the network
// work described by that plan.
type Edge struct {
	planner     Planner
	executor    Executor
	automation  automation.Executor
	schedules   automation.ScheduleRegistrar
	maxBodySize int64
	passThrough http.Handler
}

// SetScheduleRegistrar connects declarative script schedule commands to the
// Planner control plane. Scripts never receive the registrar itself.
func (e *Edge) SetScheduleRegistrar(registrar automation.ScheduleRegistrar) *Edge {
	e.schedules = registrar
	return e
}

// SetAutomationExecutor installs the optional language registry used by this
// edge. Without one, automation plans fail closed.
func (e *Edge) SetAutomationExecutor(executor automation.Executor) *Edge {
	e.automation = executor
	return e
}

func NewEdge(planner Planner, executor Executor, maxBodySize int64) *Edge {
	return &Edge{planner: planner, executor: executor, maxBodySize: maxBodySize}
}

func (e *Edge) SetPassThrough(handler http.Handler) *Edge {
	e.passThrough = handler
	return e
}

func (e *Edge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/email/submissions" {
		// Email credentials must reach only the application-owned submission
		// controller, never the ordinary route planner or a subscription.
		if e.passThrough == nil {
			http.Error(w, "email submission is not configured", http.StatusServiceUnavailable)
			return
		}
		limit := e.maxBodySize*2 + 65536
		if e.maxBodySize <= 0 {
			limit = 20*1024*1024 + 65536
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil || int64(len(body)) > limit {
			http.Error(w, "email submission body is too large or invalid", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		e.passThrough.ServeHTTP(w, r)
		return
	}
	if e.planner == nil || e.executor == nil {
		http.Error(w, "relay is not configured", http.StatusServiceUnavailable)
		return
	}

	if e.maxBodySize > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, e.maxBodySize)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "request body too large or invalid", http.StatusBadRequest)
		return
	}

	headers := cloneHeaders(r.Header)
	headers["X-Webhook-Forwarder-Delivery-Id"] = []string{deliveryID(r, body)}
	var receiptID [16]byte
	if _, err := rand.Read(receiptID[:]); err != nil {
		http.Error(w, "request identity unavailable", http.StatusServiceUnavailable)
		return
	}
	ingress := IngressRequest{
		DeliveryID: headers["X-Webhook-Forwarder-Delivery-Id"][0],
		Protocol:   ProtocolHTTP,
		Event:      EventRequest,
		Method:     r.Method,
		Scheme:     requestScheme(r),
		Host:       r.Host,
		Path:       r.URL.Path,
		RawQuery:   r.URL.RawQuery,
		Headers:    headers,
		Body:       body,
		Attributes: map[string]string{"ingress_id": hex.EncodeToString(receiptID[:]), "remote_addr": r.RemoteAddr},
		Runtime: RuntimeCapabilities{
			AutomationLanguages: automation.Languages(e.automation),
		},
	}

	plan, err := e.planner.Plan(r.Context(), ingress)
	if err != nil {
		log.Printf("Gateway route planner failed: %v", err)
		http.Error(w, "route planning failed", http.StatusBadGateway)
		return
	}
	if err := plan.Validate(); err != nil {
		http.Error(w, "invalid route plan", http.StatusBadGateway)
		return
	}

	if plan.Action == ActionPassThrough {
		if e.passThrough == nil {
			http.Error(w, "relay pass-through is not configured", http.StatusServiceUnavailable)
			return
		}
		// Planning consumed the body. Restore it before Laravel receives the
		// original request so pass-through routes behave normally.
		r.Body = io.NopCloser(bytes.NewReader(body))
		e.passThrough.ServeHTTP(w, r)
		return
	}

	if plan.Action == ActionRespond {
		writeResponse(w, *plan.ImmediateResponse)
		return
	}

	if plan.Action == ActionAutomation {
		if plan.Automation != nil && strings.HasPrefix(plan.Automation.Source, "ssh:") &&
			shellStreamMode(r, plan.Automation.Language) != "" {
			http.Error(w, "SSH Shell replies do not support streaming", http.StatusUnprocessableEntity)
			return
		}
		if e.automation == nil {
			http.Error(w, "automation execution failed", http.StatusBadGateway)
			return
		}
		script := automation.Script{
			Language: plan.Automation.Language,
			Source:   plan.Automation.Source,
			Executor: plan.Automation.Executor,
		}
		input := automation.Input{
			Event: map[string]any{
				"delivery_id": ingress.DeliveryID,
				"method":      ingress.Method,
				"scheme":      ingress.Scheme,
				"host":        ingress.Host,
				"path":        ingress.Path,
				"raw_query":   ingress.RawQuery,
				"headers":     ingress.Headers,
				"body":        string(ingress.Body),
			},
			Variables: map[string]any{
				"request": map[string]any{
					"method":  ingress.Method,
					"path":    ingress.Path,
					"query":   ingress.RawQuery,
					"headers": ingress.Headers,
					"content": string(ingress.Body),
				},
			},
		}
		if mode := shellStreamMode(r, script.Language); mode != "" {
			if len(plan.Relays) > 0 {
				http.Error(w, "streaming shell hooks cannot include relay deliveries", http.StatusBadGateway)
				return
			}
			e.streamAutomation(w, r, script, input, mode, plan.ShellNotification, ingress)
			return
		}
		outcome, automationErr := e.automation.Execute(r.Context(), script, input)
		if automationErr != nil {
			if errors.Is(automationErr, automation.ErrBusy) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "automation capacity exhausted", http.StatusServiceUnavailable)
				return
			}
			if errors.Is(automationErr, context.DeadlineExceeded) {
				http.Error(w, "automation execution timed out", http.StatusGatewayTimeout)
				return
			}
			if errors.Is(automationErr, context.Canceled) {
				return
			}
			var identified interface{ RunID() string }
			if errors.As(automationErr, &identified) && identified.RunID() != "" {
				w.Header().Set("X-Gateway-Shell-Run-Id", identified.RunID())
				if !e.enqueueShellNotification(plan.ShellNotification, ingress, shellNotificationHook(plan, script.Source), identified.RunID(), "failed") {
					http.Error(w, "shell notification queue unavailable", http.StatusServiceUnavailable)
					return
				}
			}
			http.Error(w, "automation execution failed", http.StatusBadGateway)
			return
		}
		if outcome.RunID != "" {
			w.Header().Set("X-Gateway-Shell-Run-Id", outcome.RunID)
		}
		if len(outcome.Schedules) > 0 {
			if e.schedules == nil || e.schedules.RegisterSchedules(r.Context(), automationOrigin(plan, ingress), outcome.Schedules) != nil {
				http.Error(w, "automation schedule registration failed", http.StatusBadGateway)
				return
			}
		}
		completionStatus := "succeeded"
		if outcome.RunFailed {
			completionStatus = "failed"
		}
		if !e.enqueueShellNotification(plan.ShellNotification, ingress, shellNotificationHook(plan, script.Source), outcome.RunID, completionStatus) {
			http.Error(w, "shell notification queue unavailable", http.StatusServiceUnavailable)
			return
		}
		relays := append(plan.Relays, automationDeliveries(outcome.Deliveries)...)
		if !e.enqueueRelays(relays, ingress) {
			http.Error(w, "relay queue unavailable", http.StatusServiceUnavailable)
			return
		}
		if outcome.Response != nil {
			writeResponse(w, Response{StatusCode: outcome.Response.Status, Headers: outcome.Response.Headers, Body: []byte(outcome.Response.Body)})
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("Request accepted"))
		return
	}

	if plan.Reply != nil {
		response, err := e.executor.Deliver(r.Context(), materializeDelivery(*plan.Reply, ingress))
		if err != nil {
			http.Error(w, "response target unavailable", http.StatusBadGateway)
			return
		}
		if !e.enqueueRelays(plan.Relays, ingress) {
			http.Error(w, "relay queue unavailable", http.StatusServiceUnavailable)
			return
		}
		writeResponse(w, response)
		return
	}

	if !e.enqueueRelays(plan.Relays, ingress) {
		http.Error(w, "relay queue unavailable", http.StatusServiceUnavailable)
		return
	}
	if plan.ImmediateResponse != nil {
		writeResponse(w, *plan.ImmediateResponse)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("Request accepted"))
}

func shellNotificationHook(plan RoutePlan, source string) string {
	if !strings.HasPrefix(source, "ssh:") {
		return source
	}
	if hook := plan.Metadata["shell_hook"]; hook != "" {
		return hook
	}
	return source
}

func shellStreamMode(r *http.Request, language automation.Language) string {
	if language != "shell" {
		return ""
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		return "sse"
	}
	if strings.EqualFold(r.Header.Get("X-Hook-Mode"), "chunked") {
		return "chunked"
	}
	return ""
}

func (e *Edge) streamAutomation(w http.ResponseWriter, r *http.Request, script automation.Script, input automation.Input, mode string, notification *Delivery, ingress IngressRequest) {
	flusher, ok := w.(http.Flusher)
	streamer, supported := e.automation.(automation.StreamingExecutor)
	if !ok || !supported {
		http.Error(w, "automation streaming is unavailable", http.StatusBadGateway)
		return
	}
	stream, err := streamer.OpenStream(r.Context(), script, input)
	if err != nil {
		if errors.Is(err, automation.ErrBusy) {
			http.Error(w, "automation capacity exhausted", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "automation execution failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("X-Gateway-Shell-Run-Id", stream.ID)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if mode == "sse" {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	for chunk := range stream.Chunks {
		if mode == "sse" {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", strings.ReplaceAll(strings.ReplaceAll(string(chunk), "\r", ""), "\n", "\ndata: "))
		} else {
			_, _ = w.Write(chunk)
		}
		flusher.Flush()
	}
	streamErr := <-stream.Done
	status := "succeeded"
	if streamErr != nil {
		status = "failed"
	}
	if !e.enqueueShellNotification(notification, ingress, script.Source, stream.ID, status) {
		log.Printf("shell notification queue unavailable for run %s", stream.ID)
	}
	if streamErr != nil && r.Context().Err() == nil {
		if mode == "sse" {
			_, _ = io.WriteString(w, "event: error\ndata: hook execution failed\n\n")
		} else {
			_, _ = io.WriteString(w, "\n[hook execution failed]\n")
		}
		flusher.Flush()
	}
}

func (e *Edge) enqueueShellNotification(target *Delivery, ingress IngressRequest, hook, runID, status string) bool {
	if target == nil {
		return true
	}
	if runID == "" || e.executor == nil {
		return false
	}
	body, err := json.Marshal(map[string]string{
		"event": "shell.run.completed", "run_id": runID,
		"delivery_id": ingress.DeliveryID, "hook": hook, "status": status,
	})
	if err != nil {
		return false
	}
	notice := *target
	notice.EventID = ingress.DeliveryID
	notice.Method = http.MethodPost
	notice.RawQuery = ""
	notice.Body = body
	notice.Headers = cloneHeaders(target.Headers)
	if notice.Headers == nil {
		notice.Headers = make(map[string][]string)
	}
	for name := range notice.Headers {
		if strings.EqualFold(name, "Content-Length") || strings.EqualFold(name, "Transfer-Encoding") || strings.EqualFold(name, "Content-Type") || strings.EqualFold(name, "Idempotency-Key") {
			delete(notice.Headers, name)
		}
	}
	notice.Headers["Content-Type"] = []string{"application/json"}
	notice.Headers["Idempotency-Key"] = []string{"gateway-shell-run-" + runID}
	return e.executor.Enqueue(notice)
}

func automationOrigin(plan RoutePlan, ingress IngressRequest) string {
	if endpointID := strings.TrimSpace(plan.Metadata["endpoint_id"]); endpointID != "" {
		return "endpoint:" + endpointID
	}
	sum := sha256.Sum256([]byte(ingress.Host + "\n" + ingress.Path))
	return fmt.Sprintf("route:%x", sum)
}

func automationDeliveries(deliveries []automation.Delivery) []Delivery {
	converted := make([]Delivery, 0, len(deliveries))
	for _, delivery := range deliveries {
		converted = append(converted, Delivery{
			URL:     delivery.URL,
			Method:  delivery.Method,
			Headers: delivery.Headers,
			Body:    []byte(delivery.Body),
		})
	}
	return converted
}

func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func (e *Edge) enqueueRelays(relays []Delivery, ingress IngressRequest) bool {
	for _, relay := range relays {
		if !e.executor.Enqueue(materializeDelivery(relay, ingress)) {
			return false
		}
	}
	return true
}

func materializeDelivery(delivery Delivery, ingress IngressRequest) Delivery {
	if delivery.EventID == "" {
		delivery.EventID = ingress.Attributes["ingress_id"]
		if delivery.EventID == "" {
			delivery.EventID = ingress.DeliveryID
		}
	}
	if delivery.Method == "" {
		delivery.Method = ingress.Method
	}
	if delivery.RawQuery == "" {
		delivery.RawQuery = ingress.RawQuery
	}
	if delivery.Headers == nil {
		delivery.Headers = cloneHeaders(ingress.Headers)
	}
	if delivery.Body == nil {
		delivery.Body = append([]byte(nil), ingress.Body...)
	}
	return delivery
}

func deliveryID(r *http.Request, body []byte) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, r.Method)
	_, _ = io.WriteString(hash, "\n"+r.URL.RequestURI())
	_, _ = hash.Write(body)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func cloneHeaders(headers http.Header) map[string][]string {
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func writeResponse(w http.ResponseWriter, response Response) {
	connectionHeaders := make(map[string]struct{})
	for key, values := range response.Headers {
		if http.CanonicalHeaderKey(key) != "Connection" {
			continue
		}
		for _, value := range values {
			for _, name := range strings.Split(value, ",") {
				connectionHeaders[http.CanonicalHeaderKey(strings.TrimSpace(name))] = struct{}{}
			}
		}
	}
	for key, values := range response.Headers {
		canonicalKey := http.CanonicalHeaderKey(key)
		if _, listedByConnection := connectionHeaders[canonicalKey]; listedByConnection || hopByHopHeaders[canonicalKey] {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	statusCode := response.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(response.Body)
}

var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Content-Length":      true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

var _ http.Handler = (*Edge)(nil)
