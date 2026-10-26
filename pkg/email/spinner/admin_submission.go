package spinner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	protocol "github.com/webong/gateway/arranger/integration"
	emailattrs "github.com/webong/gateway/pkg/email/spinner/attributes"
	"github.com/webong/gateway/pkg/email/spinner/smtp"
)

// SubmissionQueue is the durable handoff required by authenticated Email
// submission. The interface stays transport-neutral so Email owns its policy
// without depending on a particular Spinner queue implementation.
type SubmissionQueue interface {
	EnqueueGatewayIdempotent(protocol.GatewayDelivery, string, string, time.Time) (bool, error)
}

// SigningChecker verifies that a Gateway-managed sender has a usable key.
type SigningChecker interface {
	CheckSigningDomain(string) error
}

type adminSubmissionHandler struct {
	queue          SubmissionQueue
	checker        SigningChecker
	scanner        smtp.MalwareScanner
	maxMessageSize int64
	loopKey        []byte
	failureNotices bool
}

// NewAdminSubmissionHandler builds Email's authenticated durable handoff. A
// host chooses where to mount it and owns authentication for that private
// listener; all Email validation remains inside this package.
func NewAdminSubmissionHandler(queue SubmissionQueue, checker SigningChecker, scanner smtp.MalwareScanner, maxMessageSize int64, loopKey []byte, failureNotices bool) (http.Handler, error) {
	if queue == nil || maxMessageSize < 1 || len(loopKey) == 0 {
		return nil, errors.New("email submission requires a durable queue, size limit and loop key")
	}
	return &adminSubmissionHandler{
		queue: queue, checker: checker, scanner: scanner, maxMessageSize: maxMessageSize,
		loopKey: append([]byte(nil), loopKey...), failureNotices: failureNotices,
	}, nil
}

func (handler *adminSubmissionHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
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
	limit := handler.maxMessageSize*2 + 65536
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
	if int64(len(input.Message)) > handler.maxMessageSize {
		writeError(writer, http.StatusRequestEntityTooLarge, "Email message is too large.")
		return
	}
	message, signingDomain, err := smtp.PrepareAuthenticatedSubmission(input.MailFrom, input.AuthorizedSenders, input.Recipients, input.Message, handler.loopKey)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "Email submission is not authorized or valid.")
		return
	}
	// A mailbox connection provides its own SMTP transport and signing policy.
	// Only Gateway-managed SMTP deliveries require a Planner-managed DKIM key.
	gatewayManagedSigning := input.MailboxConnectionID == ""
	if gatewayManagedSigning && handler.checker != nil && handler.checker.CheckSigningDomain(signingDomain) != nil {
		writeError(writer, http.StatusServiceUnavailable, "Email signing key is unavailable.")
		return
	}
	if handler.scanner != nil {
		verdict, scanErr := handler.scanner.Scan(request.Context(), input.Message)
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
		"mail_from":  input.MailFrom,
		"recipients": string(recipients),
	}
	if gatewayManagedSigning {
		attributes[emailattrs.DKIMSigningDomain] = signingDomain
	}
	adapter := ""
	if input.MailboxConnectionID != "" {
		if !validMailboxConnectionID(input.MailboxConnectionID) {
			writeError(writer, http.StatusUnprocessableEntity, "Mailbox connection is invalid.")
			return
		}
		attributes["mailbox_connection_id"] = input.MailboxConnectionID
		adapter = "mailbox.smtp"
		supported, ok := handler.queue.(interface{ SupportsAdapter(string) bool })
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
		if supported, ok := handler.queue.(interface{ SupportsAdapter(string) bool }); !ok || !supported.SupportsAdapter(adapter) {
			writeError(writer, http.StatusServiceUnavailable, "Gateway outbound transport is unavailable.")
			return
		}
	} else if supported, ok := handler.queue.(interface{ SupportsAdapter(string) bool }); ok && !supported.SupportsAdapter("smtp") {
		writeError(writer, http.StatusServiceUnavailable, "Gateway outbound SMTP transport is unavailable.")
		return
	}
	if handler.failureNotices && adapter == "" {
		attributes[emailattrs.FailureNoticeTo] = input.MailFrom
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
	replayed, err := handler.queue.EnqueueGatewayIdempotent(protocol.GatewayDelivery{
		Protocol:   protocol.ProtocolSMTP,
		Adapter:    adapter,
		Target:     input.Recipients[0],
		Payload:    message,
		Attributes: attributes,
	}, input.ReceiptID, hex.EncodeToString(fingerprint[:]), time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		if errors.Is(err, protocol.ErrIdempotencyConflict) {
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
