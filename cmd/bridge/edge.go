package webrelay

// Edge is the Go-side HTTP adapter mounted in RoadRunner's HTTP pipeline.

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Edge is the Go-side HTTP adapter mounted in RoadRunner's HTTP pipeline. It
// captures the request, asks PHP for a plan, and executes only the network
// work described by that plan.
type Edge struct {
	planner     Planner
	executor    Executor
	maxBodySize int64
	passThrough http.Handler
}

func NewEdge(planner Planner, executor Executor, maxBodySize int64) *Edge {
	return &Edge{planner: planner, executor: executor, maxBodySize: maxBodySize}
}

func (e *Edge) SetPassThrough(handler http.Handler) *Edge {
	e.passThrough = handler
	return e
}

func (e *Edge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	ingress := IngressRequest{
		DeliveryID: headers["X-Webhook-Forwarder-Delivery-Id"][0],
		Method:     r.Method,
		Scheme:     requestScheme(r),
		Host:       r.Host,
		Path:       r.URL.Path,
		RawQuery:   r.URL.RawQuery,
		Headers:    headers,
		Body:       body,
	}

	plan, err := e.planner.Plan(r.Context(), ingress)
	if err != nil {
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

	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("Request accepted"))
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
