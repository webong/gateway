// Package deliveryadmin exposes spool inspection and failed-delivery redrive
// on a separately configured private listener, never on the public edge.
package deliveryadmin

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/webong/gateway/driver/automation"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/cmd/bridge/deliveryqueue"
	"github.com/webong/gateway/src/spinner/cmd/bridge/smtp"
)

type queue interface {
	Status(string) (deliveryqueue.Status, error)
	List(deliveryqueue.State, int) ([]deliveryqueue.Status, error)
	ListEvent(string, int) ([]deliveryqueue.Status, error)
	Retry(string) error
}

type Handler struct {
	queue      queue
	token      string
	email      *emailSubmission
	runs       runHistoryReader
	automation automation.Executor
}

type runHistoryReader interface {
	ReadRun(string) ([]byte, error)
}

type emailQueue interface {
	EnqueueGatewayIdempotent(bridge.GatewayDelivery, string, string, time.Time) (bool, error)
}

type signingChecker interface {
	CheckSigningDomain(string) error
}

type emailSubmission struct {
	queue          emailQueue
	checker        signingChecker
	scanner        smtp.MalwareScanner
	maxMessageSize int64
	loopKey        []byte
	failureNotices bool
}

type HandlerOption func(*Handler) error

// WithAutomationExecutor exposes synchronous, declarative script execution on
// the private admin listener. Scripts return a request plan; they never receive
// the registered credential or a network client.
func WithAutomationExecutor(executor automation.Executor) HandlerOption {
	return func(handler *Handler) error {
		if executor == nil {
			return errors.New("automation executor is required")
		}
		handler.automation = executor
		return nil
	}
}

// WithShellRunHistory exposes redacted shell-run records on the private admin
// listener. The reader enforces run ID and file safety checks.
func WithShellRunHistory(reader runHistoryReader) HandlerOption {
	return func(handler *Handler) error {
		if reader == nil {
			return errors.New("shell run history reader is required")
		}
		handler.runs = reader
		return nil
	}
}

// WithEmailSubmission enables the private handoff when a durable SMTP adapter
// is present on this node, including per-connection mailbox SMTP routing.
func WithEmailSubmission(queue emailQueue, checker signingChecker, scanner smtp.MalwareScanner, maxMessageSize int64, loopKey []byte, failureNotices bool) HandlerOption {
	return func(handler *Handler) error {
		if queue == nil || maxMessageSize < 1 || len(loopKey) == 0 {
			return errors.New("email submission requires a durable queue, size limit and loop key")
		}
		handler.email = &emailSubmission{queue: queue, checker: checker, scanner: scanner, maxMessageSize: maxMessageSize, loopKey: append([]byte(nil), loopKey...), failureNotices: failureNotices}
		return nil
	}
}

func NewHandler(queue queue, token string, options ...HandlerOption) (*Handler, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("private admin requires an internal token")
	}
	handler := &Handler{queue: queue, token: token}
	for _, option := range options {
		if err := option(handler); err != nil {
			return nil, err
		}
	}
	if queue == nil && handler.runs == nil && handler.automation == nil {
		return nil, errors.New("private admin requires a delivery queue, shell run history or automation executor")
	}
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
	if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(handler.token)) != 1 {
		writeError(writer, http.StatusUnauthorized, "Unauthenticated.")
		return
	}
	path := request.URL.Path
	if path == "/automation/credential-hook" {
		handler.executeCredentialHook(writer, request)
		return
	}
	if strings.HasPrefix(path, "/shell/runs/") {
		if handler.runs == nil {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		record, err := handler.runs.ReadRun(strings.TrimPrefix(path, "/shell/runs/"))
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(writer, request)
			return
		}
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "Shell run history is unavailable.")
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(record)
		return
	}
	if handler.queue == nil {
		http.NotFound(writer, request)
		return
	}
	if path == "/email/submissions" {
		handler.submitEmail(writer, request)
		return
	}
	if path == "/deliveries" {
		if request.Method != http.MethodGet {
			methodNotAllowed(writer, http.MethodGet)
			return
		}
		state := deliveryqueue.State(request.URL.Query().Get("state"))
		eventID := request.URL.Query().Get("event_id")
		if _, present := request.URL.Query()["event_id"]; present {
			if !validEventID(eventID) || request.URL.Query().Has("state") {
				writeError(writer, http.StatusBadRequest, "Invalid delivery event filter.")
				return
			}
		} else if state == "" {
			state = deliveryqueue.StateFailed
		}
		limit := 50
		if raw := request.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 100 {
				writeError(writer, http.StatusBadRequest, "Limit must be between 1 and 100.")
				return
			}
			limit = parsed
		}
		var statuses []deliveryqueue.Status
		var err error
		if eventID != "" {
			statuses, err = handler.queue.ListEvent(eventID, limit)
		} else {
			statuses, err = handler.queue.List(state, limit)
		}
		if errors.Is(err, deliveryqueue.ErrInvalidState) {
			writeError(writer, http.StatusBadRequest, "Unsupported delivery state.")
			return
		}
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "Delivery status is unavailable.")
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"data": statuses})
		return
	}
	if !strings.HasPrefix(path, "/deliveries/") {
		http.NotFound(writer, request)
		return
	}
	rest := strings.TrimPrefix(path, "/deliveries/")
	if strings.HasSuffix(rest, "/retry") {
		id := strings.TrimSuffix(rest, "/retry")
		if request.Method != http.MethodPost {
			methodNotAllowed(writer, http.MethodPost)
			return
		}
		if request.ContentLength > 0 {
			writeError(writer, http.StatusBadRequest, "Retry does not accept a request body.")
			return
		}
		if err := handler.queue.Retry(id); err != nil {
			writeQueueError(writer, err)
			return
		}
		status, err := handler.queue.Status(id)
		if err != nil {
			writeError(writer, http.StatusInternalServerError, "Delivery status is unavailable.")
			return
		}
		writeJSON(writer, http.StatusAccepted, status)
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	status, err := handler.queue.Status(rest)
	if err != nil {
		writeQueueError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (handler *Handler) executeCredentialHook(writer http.ResponseWriter, request *http.Request) {
	if handler.automation == nil {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	if request.Header.Get("Content-Type") != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "JSON content type is required.")
		return
	}
	var input struct {
		Language string         `json:"language"`
		Source   string         `json:"source"`
		Event    map[string]any `json:"event"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 131072))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF ||
		len(input.Language) > 64 || len(input.Source) > 65536 || input.Event == nil {
		writeError(writer, http.StatusBadRequest, "Invalid credential hook.")
		return
	}
	outcome, err := handler.automation.Execute(request.Context(), automation.Script{
		Language: automation.Language(input.Language), Source: input.Source,
	}, automation.Input{Event: input.Event})
	if err != nil || len(outcome.Deliveries) != 1 || len(outcome.Schedules) != 0 || outcome.Response != nil {
		writeError(writer, http.StatusUnprocessableEntity, "Credential hook did not return one request.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"delivery": outcome.Deliveries[0]})
}

func (handler *Handler) submitEmail(writer http.ResponseWriter, request *http.Request) {
	if handler.email == nil {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	if request.Header.Get("Content-Type") != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "JSON content type is required.")
		return
	}
	var input struct {
		ReceiptID           string   `json:"receipt_id"`
		MailFrom            string   `json:"mail_from"`
		AuthorizedSenders   []string `json:"authorized_senders"`
		Recipients          []string `json:"recipients"`
		Message             []byte   `json:"message"`
		MailboxConnectionID string   `json:"mailbox_connection_id"`
		OutboundBinding     *struct {
			PoolID       string `json:"pool_id"`
			AccountID    string `json:"account_id"`
			RouteID      string `json:"route_id"`
			DomainID     string `json:"domain_id"`
			SenderDomain string `json:"sender_domain"`
		} `json:"outbound_binding"`
	}
	// Base64 expands the raw RFC 5322 message; leave room for the JSON envelope.
	limit := handler.email.maxMessageSize*2 + 65536
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(any)) != io.EOF {
		writeError(writer, http.StatusBadRequest, "Invalid email submission.")
		return
	}
	if len(input.ReceiptID) != 32 || !validReceiptID(input.ReceiptID) {
		writeError(writer, http.StatusBadRequest, "Invalid email submission receipt.")
		return
	}
	if int64(len(input.Message)) > handler.email.maxMessageSize {
		writeError(writer, http.StatusRequestEntityTooLarge, "Email message is too large.")
		return
	}
	message, signingDomain, err := smtp.PrepareAuthenticatedSubmission(input.MailFrom, input.AuthorizedSenders, input.Recipients, input.Message, handler.email.loopKey)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "Email submission is not authorized or valid.")
		return
	}
	if handler.email.checker != nil && handler.email.checker.CheckSigningDomain(signingDomain) != nil {
		writeError(writer, http.StatusServiceUnavailable, "Email signing key is unavailable.")
		return
	}
	if handler.email.scanner != nil {
		verdict, scanErr := handler.email.scanner.Scan(request.Context(), input.Message)
		if scanErr != nil {
			writeError(writer, http.StatusServiceUnavailable, "Email malware scanner is unavailable.")
			return
		}
		if verdict.Infected {
			writeError(writer, http.StatusUnprocessableEntity, "Email submission was rejected.")
			return
		}
	}
	recipients, _ := json.Marshal(input.Recipients)
	attributes := map[string]string{
		"mail_from":                       input.MailFrom,
		"recipients":                      string(recipients),
		bridge.DKIMSigningDomainAttribute: signingDomain,
	}
	adapter := ""
	if input.MailboxConnectionID != "" {
		if !validMailboxConnectionID(input.MailboxConnectionID) {
			writeError(writer, http.StatusUnprocessableEntity, "Mailbox connection is invalid.")
			return
		}
		attributes["mailbox_connection_id"] = input.MailboxConnectionID
		adapter = "mailbox.smtp"
		supported, ok := handler.email.queue.(interface{ SupportsAdapter(string) bool })
		if !ok || !supported.SupportsAdapter(adapter) {
			writeError(writer, http.StatusServiceUnavailable, "Mailbox SMTP transport is unavailable.")
			return
		}
	} else if input.OutboundBinding != nil {
		binding := input.OutboundBinding
		if !validMailboxConnectionID(binding.PoolID) || !validMailboxConnectionID(binding.RouteID) ||
			(binding.DomainID != "" && !validMailboxConnectionID(binding.DomainID)) ||
			binding.AccountID == "" || len(binding.AccountID) > 200 || binding.SenderDomain != signingDomain {
			writeError(writer, http.StatusUnprocessableEntity, "Invalid outbound binding.")
			return
		}
		adapter = "managed.smtp"
		for key, value := range map[string]string{
			"outbound_pool_id": binding.PoolID, "account_id": binding.AccountID,
			"outbound_route_id": binding.RouteID, "outbound_domain_id": binding.DomainID,
			"outbound_sender_domain": binding.SenderDomain,
		} {
			attributes[key] = value
		}
		if supported, ok := handler.email.queue.(interface{ SupportsAdapter(string) bool }); !ok || !supported.SupportsAdapter(adapter) {
			writeError(writer, http.StatusServiceUnavailable, "Gateway outbound transport is unavailable.")
			return
		}
	} else if supported, ok := handler.email.queue.(interface{ SupportsAdapter(string) bool }); ok && !supported.SupportsAdapter("smtp") {
		writeError(writer, http.StatusServiceUnavailable, "Gateway outbound SMTP transport is unavailable.")
		return
	}
	if handler.email.failureNotices && adapter == "" {
		attributes[bridge.FailureNoticeToAttribute] = input.MailFrom
	}
	fingerprintInput, _ := json.Marshal(struct {
		MailFrom   string   `json:"mail_from"`
		Recipients []string `json:"recipients"`
		Message    []byte   `json:"message"`
	}{input.MailFrom, input.Recipients, input.Message})
	if adapter != "" {
		fingerprintInput = append(fingerprintInput, []byte(input.MailboxConnectionID)...)
	}
	fingerprint := sha256.Sum256(fingerprintInput)
	replayed, err := handler.email.queue.EnqueueGatewayIdempotent(bridge.GatewayDelivery{
		Protocol:   bridge.ProtocolSMTP,
		Adapter:    adapter,
		Target:     input.Recipients[0],
		Payload:    message,
		Attributes: attributes,
	}, input.ReceiptID, hex.EncodeToString(fingerprint[:]), time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		if errors.Is(err, deliveryqueue.ErrIdempotencyConflict) {
			writeError(writer, http.StatusConflict, "Email submission receipt conflicts with another message.")
			return
		}
		writeError(writer, http.StatusServiceUnavailable, "Email delivery queue is unavailable.")
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"id": input.ReceiptID, "state": "queued", "replayed": replayed})
}

func validMailboxConnectionID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, char := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validReceiptID(id string) bool {
	for _, char := range id {
		if char < '0' || (char > '9' && char < 'a') || char > 'f' {
			return false
		}
	}
	return true
}

func validEventID(value string) bool {
	if len(value) < 1 || len(value) > 255 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._:-", char) {
			continue
		}
		return false
	}
	return true
}

func writeQueueError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, deliveryqueue.ErrNotFound):
		writeError(writer, http.StatusNotFound, "Delivery not found.")
	case errors.Is(err, deliveryqueue.ErrAlreadyPending):
		writeError(writer, http.StatusConflict, "Delivery is already pending.")
	default:
		writeError(writer, http.StatusInternalServerError, "Delivery operation failed.")
	}
}

func methodNotAllowed(writer http.ResponseWriter, allowed string) {
	writer.Header().Set("Allow", allowed)
	writeError(writer, http.StatusMethodNotAllowed, "Method not allowed.")
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"message": message})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
