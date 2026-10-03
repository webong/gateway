package deliveryqueue

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

func validFailureNoticeDomain(domain string) bool {
	if len(domain) < 3 || len(domain) > 253 || strings.ContainsAny(domain, "\r\n\t @") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return strings.Contains(domain, ".")
}

func validFailureNoticeAddress(address string) bool {
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

func failureNoticeRecipients(delivery bridge.GatewayDelivery) ([]string, error) {
	recipients := []string{delivery.Target}
	if raw := delivery.Attributes["recipients"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &recipients); err != nil {
			return nil, errors.New("invalid failure notice recipient batch")
		}
	}
	if len(recipients) < 1 || len(recipients) > 100 || recipients[0] != delivery.Target {
		return nil, errors.New("invalid failure notice recipient batch")
	}
	for _, recipient := range recipients {
		if !validFailureNoticeAddress(recipient) {
			return nil, errors.New("invalid failure notice recipient address")
		}
	}
	return recipients, nil
}

func failureNoticeID(originalID string) string {
	digest := sha256.Sum256([]byte("gateway-failure-notice:" + originalID))
	return hex.EncodeToString(digest[:16])
}

func failureNoticeDelivery(original item, domain string) (bridge.GatewayDelivery, error) {
	to := original.Delivery.Attributes[bridge.FailureNoticeToAttribute]
	if !validFailureNoticeAddress(to) || to != original.Delivery.Attributes["mail_from"] {
		return bridge.GatewayDelivery{}, errors.New("invalid failure notice reverse path")
	}
	recipients, err := failureNoticeRecipients(original.Delivery)
	if err != nil {
		return bridge.GatewayDelivery{}, err
	}
	id := failureNoticeID(original.ID)
	boundary := "gateway-dsn-" + id
	var message strings.Builder
	fmt.Fprintf(&message, "From: Gateway Postmaster <postmaster@%s>\r\n", domain)
	fmt.Fprintf(&message, "To: <%s>\r\n", to)
	fmt.Fprintf(&message, "Date: %s\r\n", original.UpdatedAt.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&message, "Message-ID: <%s@%s>\r\n", id, domain)
	message.WriteString("Subject: Delivery Status Notification (Failure)\r\n")
	message.WriteString("Auto-Submitted: auto-generated\r\n")
	message.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&message, "Content-Type: multipart/report; report-type=delivery-status; boundary=\"%s\"\r\n\r\n", boundary)
	fmt.Fprintf(&message, "--%s\r\nContent-Type: text/plain; charset=us-ascii\r\n\r\n", boundary)
	fmt.Fprintf(&message, "Gateway could not complete delivery after bounded retries.\r\nDelivery ID: %s\r\n\r\n", original.ID)
	fmt.Fprintf(&message, "--%s\r\nContent-Type: message/delivery-status\r\n\r\n", boundary)
	fmt.Fprintf(&message, "Reporting-MTA: dns; %s\r\n", domain)
	fmt.Fprintf(&message, "Arrival-Date: %s\r\n\r\n", original.CreatedAt.UTC().Format(time.RFC1123Z))
	for _, recipient := range recipients {
		fmt.Fprintf(&message, "Final-Recipient: rfc822; %s\r\n", recipient)
		message.WriteString("Action: failed\r\nStatus: 5.0.0\r\n")
		message.WriteString("Diagnostic-Code: smtp; Gateway delivery failed after retries\r\n")
		fmt.Fprintf(&message, "Last-Attempt-Date: %s\r\n\r\n", original.UpdatedAt.UTC().Format(time.RFC1123Z))
	}
	fmt.Fprintf(&message, "--%s--\r\n", boundary)
	attributes := map[string]string{"mail_from": ""}
	if original.EventID != "" {
		attributes["event_id"] = original.EventID
	}
	return bridge.GatewayDelivery{
		Protocol: bridge.ProtocolSMTP, Target: to, SubscriberID: "gateway-failure-notice",
		Attributes: attributes, Payload: []byte(message.String()),
	}, nil
}

func (queue *Queue) failureNoticeExists(id string) (bool, error) {
	for _, directory := range []string{queue.pendingDir, queue.failedDir, queue.deliveredDir} {
		_, err := os.Stat(filepath.Join(directory, id+".json"))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

// ensureFailureNotice is idempotent across a crash between enqueueing the
// notice and marking the original failed item. The notice uses a deterministic
// spool ID, a null reverse-path, and never requests another notice.
func (queue *Queue) ensureFailureNotice(original item) error {
	if original.Delivery.Attributes[bridge.FailureNoticeToAttribute] == "" || original.FailureNoticeID != "" {
		return nil
	}
	if !validFailureNoticeDomain(queue.config.FailureNoticeDomain) || queue.config.Adapters[string(bridge.ProtocolSMTP)] == nil {
		return errors.New("failure notice relay is unavailable")
	}
	queue.transitionMu.Lock()
	defer queue.transitionMu.Unlock()
	path := filepath.Join(queue.failedDir, original.ID+".json")
	current, err := readItem(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // An operator may have retried the original delivery.
	}
	if err != nil || current.State != StateFailed || current.FailureNoticeID != "" {
		return err
	}
	id := failureNoticeID(current.ID)
	exists, err := queue.failureNoticeExists(id)
	if err != nil {
		return err
	}
	if !exists {
		delivery, err := failureNoticeDelivery(current, queue.config.FailureNoticeDomain)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		notice := item{
			Version: spoolVersion, ID: id, EventID: current.EventID, Delivery: delivery,
			Protocol: bridge.ProtocolSMTP, Adapter: string(bridge.ProtocolSMTP),
			SubscriberID: delivery.SubscriberID, State: StateQueued, NextAttemptAt: now,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := writeItem(filepath.Join(queue.pendingDir, id+".json"), notice); err != nil {
			return err
		}
		queue.observe(notice, StateQueued)
		select {
		case queue.wake <- struct{}{}:
		default:
		}
	}
	current.FailureNoticeID = id
	return writeItem(path, current)
}

func (queue *Queue) processFailureNotices() error {
	entries, err := os.ReadDir(queue.failedDir)
	if err != nil {
		return err
	}
	var firstError error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		original, err := readItem(filepath.Join(queue.failedDir, entry.Name()))
		if err != nil {
			if firstError == nil {
				firstError = err
			}
			continue
		}
		if err := queue.ensureFailureNotice(original); err != nil {
			if firstError == nil {
				firstError = err
			}
		}
	}
	return firstError
}
