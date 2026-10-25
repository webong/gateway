package smtp

import (
	"errors"
	"net/mail"
	"strings"
)

// PrepareAuthenticatedSubmission applies the same exact sender and visible
// author rules as SMTP AUTH before a private HTTP handoff reaches the spool.
// The returned bytes carry Gateway's signed loop marker and are ready to sign
// immediately before the outbound relay attempt.
func PrepareAuthenticatedSubmission(mailFrom string, authorizedSenders, recipients []string, message, loopKey []byte) ([]byte, string, error) {
	if len(message) == 0 || len(authorizedSenders) == 0 || len(authorizedSenders) > 100 || len(recipients) == 0 || len(recipients) > 100 {
		return nil, "", errors.New("invalid email submission")
	}
	allowed := false
	for _, sender := range authorizedSenders {
		if !exactMailbox(sender) {
			return nil, "", errors.New("invalid authorized sender")
		}
		if sender == mailFrom {
			allowed = true
		}
	}
	if !allowed || !exactMailbox(mailFrom) {
		return nil, "", errors.New("envelope sender is not authorized")
	}
	for _, recipient := range recipients {
		if !exactMailbox(recipient) {
			return nil, "", errors.New("invalid recipient")
		}
	}
	if authorAddress(message) != mailFrom || authorDomain(message) == "" {
		return nil, "", errors.New("message author is not authorized")
	}
	if forwardingHops(message, loopKey) >= maxForwardingHops || receivedHops(message) >= maxReceivedHops {
		return nil, "", errors.New("gateway forwarding loop detected")
	}
	marked, err := markForwarding(message, loopKey)
	return marked, authorDomain(message), err
}

func exactMailbox(address string) bool {
	if address == "" || len(address) > 254 || strings.ContainsAny(address, "*\r\n<>") {
		return false
	}
	parsed, err := mail.ParseAddress(address)
	return err == nil && parsed.Address == address && parsed.Name == ""
}
