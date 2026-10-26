// Package deliveryfailure owns Email-specific terminal-failure behavior.
package deliveryfailure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	protocol "github.com/webong/gateway/arranger/integration"
	emailattrs "github.com/webong/gateway/pkg/email/spinner/attributes"
)

type Handler struct{ domain string }

func New(domain string) (*Handler, error) {
	domain = strings.TrimSpace(domain)
	if !validDomain(domain) {
		return nil, errors.New("Email failure notice domain must be a valid DNS hostname")
	}
	return &Handler{domain: domain}, nil
}

func (handler *Handler) FollowUp(_ context.Context, request protocol.DeliveryFailureRequest) (*protocol.GatewayDelivery, error) {
	if handler == nil || handler.domain == "" {
		return nil, errors.New("Email failure handler is unavailable")
	}
	to := request.Delivery.Attributes[emailattrs.FailureNoticeTo]
	if to == "" {
		return nil, nil
	}
	if !validAddress(to) || to != request.Delivery.Attributes["mail_from"] {
		return nil, errors.New("invalid Email failure notice reverse path")
	}
	recipients, err := recipients(request.Delivery)
	if err != nil {
		return nil, err
	}
	boundary := "gateway-dsn-" + request.ID
	var message strings.Builder
	fmt.Fprintf(&message, "From: Gateway Postmaster <postmaster@%s>\r\n", handler.domain)
	fmt.Fprintf(&message, "To: <%s>\r\n", to)
	fmt.Fprintf(&message, "Date: %s\r\n", request.FailedAt.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&message, "Message-ID: <%s@%s>\r\n", request.ID, handler.domain)
	message.WriteString("Subject: Delivery Status Notification (Failure)\r\n")
	message.WriteString("Auto-Submitted: auto-generated\r\n")
	message.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&message, "Content-Type: multipart/report; report-type=delivery-status; boundary=\"%s\"\r\n\r\n", boundary)
	fmt.Fprintf(&message, "--%s\r\nContent-Type: text/plain; charset=us-ascii\r\n\r\n", boundary)
	fmt.Fprintf(&message, "Gateway could not complete delivery after bounded retries.\r\nDelivery ID: %s\r\n\r\n", request.ID)
	fmt.Fprintf(&message, "--%s\r\nContent-Type: message/delivery-status\r\n\r\n", boundary)
	fmt.Fprintf(&message, "Reporting-MTA: dns; %s\r\n", handler.domain)
	fmt.Fprintf(&message, "Arrival-Date: %s\r\n\r\n", request.CreatedAt.UTC().Format(time.RFC1123Z))
	for _, recipient := range recipients {
		fmt.Fprintf(&message, "Final-Recipient: rfc822; %s\r\n", recipient)
		message.WriteString("Action: failed\r\nStatus: 5.0.0\r\n")
		message.WriteString("Diagnostic-Code: smtp; Gateway delivery failed after retries\r\n")
		fmt.Fprintf(&message, "Last-Attempt-Date: %s\r\n\r\n", request.FailedAt.UTC().Format(time.RFC1123Z))
	}
	fmt.Fprintf(&message, "--%s--\r\n", boundary)
	attributes := map[string]string{"mail_from": ""}
	if request.EventID != "" {
		attributes["event_id"] = request.EventID
	}
	return &protocol.GatewayDelivery{
		Protocol: protocol.ProtocolSMTP, Adapter: string(protocol.ProtocolSMTP), Target: to,
		SubscriberID: "email-delivery-status", Attributes: attributes, Payload: []byte(message.String()),
	}, nil
}

func recipients(delivery protocol.GatewayDelivery) ([]string, error) {
	result := []string{delivery.Target}
	if raw := delivery.Attributes["recipients"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return nil, errors.New("invalid Email failure notice recipient batch")
		}
	}
	if len(result) < 1 || len(result) > 100 || result[0] != delivery.Target {
		return nil, errors.New("invalid Email failure notice recipient batch")
	}
	for _, recipient := range result {
		if !validAddress(recipient) {
			return nil, errors.New("invalid Email failure notice recipient address")
		}
	}
	return result, nil
}

func validAddress(address string) bool {
	if address == "" || len(address) > 254 || strings.ContainsAny(address, "\r\n\x00") {
		return false
	}
	for _, character := range address {
		if character < 33 || character > 126 {
			return false
		}
	}
	parsed, err := mail.ParseAddress(address)
	return err == nil && parsed.Address == address && strings.Count(address, "@") == 1
}

func validDomain(domain string) bool {
	if len(domain) < 3 || len(domain) > 253 || strings.ContainsAny(domain, "\r\n\t @") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return strings.Contains(domain, ".")
}
