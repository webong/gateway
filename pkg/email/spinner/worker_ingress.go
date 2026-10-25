package spinner

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	protocol "github.com/webong/gateway/arranger/protocol"
)

const maxWorkerMIME = 10 << 20

const WorkerIngressPrefix = "/email/worker-ingress"

type workerIngressQueue interface {
	EnqueueGatewayIdempotent(protocol.GatewayDelivery, string, string, time.Time) (bool, error)
}

type workerIngressDecision struct {
	CallbackURL          string              `json:"callback_url"`
	Headers              map[string][]string `json:"headers"`
	PrivateCallbackCIDRs string              `json:"private_callback_cidrs"`
}

// NewWorkerIngressHandler is the public, TLS-only Email Worker seam. The
// private Planner authenticates the route; Spinner alone durably accepts MIME.
func NewWorkerIngressHandler(planner http.Handler, internalToken string, queue workerIngressQueue) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "Method not allowed.", 405)
			return
		}
		if r.TLS == nil {
			http.Error(w, "HTTPS is required.", 403)
			return
		}
		if planner == nil || internalToken == "" || queue == nil {
			http.Error(w, "Mail ingress is unavailable.", 503)
			return
		}
		routeID := strings.TrimPrefix(r.URL.Path, WorkerIngressPrefix+"/")
		if _, err := uuid.Parse(routeID); err != nil || strings.Contains(routeID, "/") {
			http.NotFound(w, r)
			return
		}
		contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || contentType != "message/rfc822" || r.ContentLength > maxWorkerMIME {
			http.Error(w, "A bounded raw MIME message is required.", 413)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || token == r.Header.Get("Authorization") || len(token) > 128 {
			http.Error(w, "Unauthenticated.", 401)
			return
		}
		mailFrom := r.Header.Get("X-Gateway-Mail-From")
		recipient := r.Header.Get("X-Gateway-Recipient")
		if recipient == "" || len(recipient) > 320 || len(mailFrom) > 320 {
			http.Error(w, "Invalid mail envelope.", 422)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxWorkerMIME+1))
		if err != nil || len(body) == 0 || len(body) > maxWorkerMIME {
			http.Error(w, "A bounded raw MIME message is required.", 413)
			return
		}
		messageHash := sha256.Sum256(body)
		messageHex := hex.EncodeToString(messageHash[:])
		identity := sha256.Sum256([]byte(routeID + "\n" + mailFrom + "\n" + recipient + "\n" + messageHex))
		eventID := hex.EncodeToString(identity[:])
		receiptID := eventID[:32]
		claim, _ := json.Marshal(map[string]string{
			"token": token, "mail_from": mailFrom, "recipient": recipient,
			"event_id": eventID, "message_sha256": messageHex,
		})
		request, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			"http://gateway.internal/_internal/gateway/worker-ingress/"+routeID+"/authorize", bytes.NewReader(claim))
		if err != nil {
			http.Error(w, "Mail ingress is unavailable.", 503)
			return
		}
		request.Header.Set("X-Gateway-Internal", internalToken)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		planner.ServeHTTP(response, request)
		if response.Code == 403 || response.Code == 404 {
			http.Error(w, "Worker ingress denied.", 403)
			return
		}
		if response.Code != 200 {
			http.Error(w, "Worker ingress is unavailable.", 503)
			return
		}
		var decision workerIngressDecision
		if err := json.Unmarshal(response.Body.Bytes(), &decision); err != nil ||
			!validWorkerDecision(decision, routeID, eventID) {
			http.Error(w, "Worker ingress decision is invalid.", 503)
			return
		}
		attributes := map[string]string{"event_id": eventID, "route_id": routeID, "method": "POST"}
		if decision.PrivateCallbackCIDRs != "" {
			attributes["private_callback_cidrs"] = decision.PrivateCallbackCIDRs
		}
		delivery := protocol.GatewayDelivery{Protocol: protocol.ProtocolHTTP, Target: decision.CallbackURL,
			SubscriberID: routeID, Headers: decision.Headers, Payload: body, Attributes: attributes}
		replayed, err := queue.EnqueueGatewayIdempotent(delivery, receiptID, eventID, time.Now().UTC().Add(24*time.Hour))
		if errors.Is(err, protocol.ErrIdempotencyConflict) {
			http.Error(w, "Mail receipt conflict.", 409)
			return
		}
		if err != nil {
			http.Error(w, "Mail delivery queue is unavailable.", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": receiptID, "event_id": eventID, "replayed": replayed})
	})
}

func validWorkerDecision(decision workerIngressDecision, routeID, eventID string) bool {
	target, err := url.Parse(decision.CallbackURL)
	if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil || target.Fragment != "" {
		return false
	}
	return len(decision.Headers["X-Gateway-Route-Id"]) == 1 && decision.Headers["X-Gateway-Route-Id"][0] == routeID &&
		len(decision.Headers["X-Gateway-Event-Id"]) == 1 && decision.Headers["X-Gateway-Event-Id"][0] == eventID &&
		len(decision.Headers["X-Gateway-Signature"]) == 1 &&
		strings.HasPrefix(decision.Headers["X-Gateway-Signature"][0], "sha256=") &&
		len(decision.Headers["X-Gateway-Envelope"]) == 1 &&
		len(decision.Headers["Content-Type"]) == 1 && decision.Headers["Content-Type"][0] == "message/rfc822"
}
