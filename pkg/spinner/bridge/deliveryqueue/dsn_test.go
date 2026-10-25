package deliveryqueue

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	smtpserver "github.com/emersion/go-smtp"
	"github.com/webong/gateway/pkg/email/spinner/smtpout"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

type failureNoticeRelay struct{ notices chan bridge.GatewayDelivery }

func (relay *failureNoticeRelay) NewSession(*smtpserver.Conn) (smtpserver.Session, error) {
	return &failureNoticeRelaySession{relay: relay}, nil
}

type failureNoticeRelaySession struct {
	relay      *failureNoticeRelay
	from       string
	recipients []string
}

func (*failureNoticeRelaySession) AuthMechanisms() []string { return nil }
func (*failureNoticeRelaySession) Auth(string) (sasl.Server, error) {
	return nil, smtpserver.ErrAuthUnsupported
}
func (session *failureNoticeRelaySession) Mail(from string, _ *smtpserver.MailOptions) error {
	session.from = from
	session.recipients = nil
	return nil
}
func (session *failureNoticeRelaySession) Rcpt(to string, _ *smtpserver.RcptOptions) error {
	if to == "inbox@example.net" {
		return &smtpserver.SMTPError{Code: 550, Message: "test relay refuses original recipient"}
	}
	session.recipients = append(session.recipients, to)
	return nil
}
func (session *failureNoticeRelaySession) Data(reader io.Reader) error {
	payload, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	session.relay.notices <- bridge.GatewayDelivery{
		Protocol: bridge.ProtocolSMTP, Target: session.recipients[0], Payload: payload,
		Attributes: map[string]string{"mail_from": session.from},
	}
	return nil
}
func (session *failureNoticeRelaySession) Reset() { session.from = ""; session.recipients = nil }
func (*failureNoticeRelaySession) Logout() error  { return nil }

func TestFailureNoticeUsesNullEnvelopeOnSMTPWire(t *testing.T) {
	relay := &failureNoticeRelay{notices: make(chan bridge.GatewayDelivery, 1)}
	server := smtpserver.NewServer(relay)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		select {
		case <-served:
		case <-time.After(time.Second):
			t.Error("test SMTP relay did not stop")
		}
	})
	sender, err := smtpout.NewSender(smtpout.Config{
		Address: listener.Addr().String(), LocalName: "gateway.example.test",
		TLSMode: smtpout.TLSNone, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := smtpout.NewGatewayAdapter(sender, nil)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := New(Config{
		Path: t.TempDir(), Adapters: map[string]bridge.GatewayDeliveryAdapter{"smtp": adapter},
		MaxAttempts: 1, PollInterval: 5 * time.Millisecond,
		FailureNoticeDomain: "gateway.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, queue) })
	delivery := testDelivery()
	delivery.Attributes[bridge.FailureNoticeToAttribute] = "sender@example.test"
	if err := queue.EnqueueGateway(delivery); err != nil {
		t.Fatal(err)
	}
	select {
	case notice := <-relay.notices:
		if notice.Attributes["mail_from"] != "" || notice.Target != "sender@example.test" {
			t.Fatalf("failure notice did not use a null SMTP reverse path: %+v", notice)
		}
		if !strings.Contains(string(notice.Payload), "Content-Type: message/delivery-status") {
			t.Fatalf("relay did not receive a DSN report: %s", notice.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("test SMTP relay did not receive the failure notice")
	}
}

func TestTerminalSMTPFailureQueuesOneNullSenderNotice(t *testing.T) {
	var originalAttempts atomic.Int32
	notices := make(chan bridge.GatewayDelivery, 2)
	adapter := bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, delivery bridge.GatewayDelivery) error {
		if delivery.Attributes["mail_from"] == "" {
			notices <- delivery
			return nil
		}
		originalAttempts.Add(1)
		return bridge.ErrPermanentDelivery
	})
	queue, err := New(Config{
		Path: t.TempDir(), Adapters: map[string]bridge.GatewayDeliveryAdapter{"smtp": adapter},
		MaxAttempts: 3, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
		DeliveryTimeout: time.Second, PollInterval: 5 * time.Millisecond,
		FailureNoticeDomain: "gateway.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, queue) })
	original := testDelivery()
	original.Attributes[bridge.FailureNoticeToAttribute] = "sender@example.test"
	original.Attributes["recipients"] = `["inbox@example.net","second@example.net"]`
	if err := queue.EnqueueGateway(original); err != nil {
		t.Fatal(err)
	}
	var failed Status
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		listed, err := queue.List(StateFailed, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, status := range listed {
			if status.FailureNoticeID != "" {
				failed = status
				break
			}
		}
		if failed.ID != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if failed.ID == "" || failed.FailureNoticeID != failureNoticeID(failed.ID) || originalAttempts.Load() != 1 {
		t.Fatalf("failed SMTP delivery did not queue one notice: %+v, attempts=%d", failed, originalAttempts.Load())
	}
	select {
	case notice := <-notices:
		if notice.Target != "sender@example.test" || notice.Attributes["mail_from"] != "" || notice.Attributes[bridge.FailureNoticeToAttribute] != "" {
			t.Fatalf("unsafe notice envelope: %+v", notice)
		}
		body := string(notice.Payload)
		for _, fragment := range []string{
			"multipart/report; report-type=delivery-status", "Reporting-MTA: dns; gateway.example.test",
			"Final-Recipient: rfc822; inbox@example.net", "Final-Recipient: rfc822; second@example.net",
			"Action: failed", "Status: 5.0.0", "Auto-Submitted: auto-generated",
		} {
			if !strings.Contains(body, fragment) {
				t.Fatalf("notice lacks %q: %s", fragment, body)
			}
		}
		if strings.Contains(body, "message\r\n") {
			t.Fatal("notice leaked the original message body")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failure notice was not delivered")
	}
	if err := queue.Retry(failed.ID); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("notified failure was retried: %v", err)
	}
	if err := queue.ensureFailureNotice(item{ID: failed.ID, Delivery: original}); err != nil {
		t.Fatal(err)
	}
	// Delivered receipts may expire; the original marker must still prevent a
	// fresh notice from being generated after that retention period.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err := queue.Status(failed.FailureNoticeID)
		if err == nil && status.State == StateDelivered {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if status, err := queue.Status(failed.FailureNoticeID); err != nil || status.State != StateDelivered {
		t.Fatalf("failure notice receipt was not delivered: %+v, %v", status, err)
	}
	// Status can observe StateDelivered in the synced pending item just before
	// the worker moves that item into the delivered receipt directory.
	receiptPath := filepath.Join(queue.deliveredDir, failed.FailureNoticeID+".json")
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(receiptPath); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.Remove(receiptPath); err != nil {
		t.Fatal(err)
	}
	if err := queue.processFailureNotices(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notices:
		t.Fatal("duplicate failure notice was sent")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFailureNoticeRecoveryRecognizesAlreadyQueuedNotice(t *testing.T) {
	path := t.TempDir()
	for _, directory := range []string{"pending", "failed", "delivered"} {
		if err := os.Mkdir(filepath.Join(path, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	delivery := testDelivery()
	delivery.Attributes[bridge.FailureNoticeToAttribute] = "sender@example.test"
	now := time.Now().UTC()
	original := item{
		Version: spoolVersion, ID: "fedcba9876543210fedcba9876543210", Delivery: delivery,
		State: StateFailed, Attempts: 1, CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	if err := writeItem(filepath.Join(path, "failed", original.ID+".json"), original); err != nil {
		t.Fatal(err)
	}
	noticeDelivery, err := failureNoticeDelivery(original, "gateway.example.test")
	if err != nil {
		t.Fatal(err)
	}
	id := failureNoticeID(original.ID)
	queued := item{
		Version: spoolVersion, ID: id, Delivery: noticeDelivery, Protocol: bridge.ProtocolSMTP,
		Adapter: "smtp", State: StateQueued, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := writeItem(filepath.Join(path, "pending", id+".json"), queued); err != nil {
		t.Fatal(err)
	}
	var noticeAttempts atomic.Int32
	queue, err := New(Config{
		Path: path, Adapters: map[string]bridge.GatewayDeliveryAdapter{"smtp": bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, incoming bridge.GatewayDelivery) error {
			if incoming.Attributes["mail_from"] != "" {
				t.Error("unexpected original SMTP retry")
			}
			noticeAttempts.Add(1)
			return nil
		})},
		FailureNoticeDomain: "gateway.example.test", PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, queue) })
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err := queue.Status(id)
		if err == nil && status.State == StateDelivered {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if delivered, err := queue.Status(id); err != nil || delivered.State != StateDelivered {
		t.Fatalf("already queued notice was not delivered: %+v, %v", delivered, err)
	}
	status, err := queue.Status(original.ID)
	if err != nil || status.FailureNoticeID != id || noticeAttempts.Load() != 1 {
		t.Fatalf("recovery duplicated or lost queued notice: %+v, attempts=%d, err=%v", status, noticeAttempts.Load(), err)
	}
}

func TestFailureNoticeRejectsUnauthenticatedOrUnsafeSpoolInput(t *testing.T) {
	queue, err := New(Config{
		Path: t.TempDir(), Adapters: map[string]bridge.GatewayDeliveryAdapter{"smtp": bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error { return nil })},
		FailureNoticeDomain: "gateway.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, queue) })
	for _, noticeTo := range []string{"victim@example.test", "sender@example.test\r\nBcc: victim@example.test"} {
		delivery := testDelivery()
		delivery.Attributes[bridge.FailureNoticeToAttribute] = noticeTo
		if err := queue.EnqueueGateway(delivery); err == nil {
			t.Fatalf("unsafe notice recipient %q was accepted", noticeTo)
		}
	}
	delivery := testDelivery()
	delivery.Attributes[bridge.FailureNoticeToAttribute] = "sender@example.test"
	delivery.Attributes["recipients"] = `["inbox@example.net","bad\r\nInjected: x"]`
	if err := queue.EnqueueGateway(delivery); err == nil {
		t.Fatal("unsafe final recipient was accepted")
	}
}

func TestFailureNoticeRecoversAfterFailedItemWasPersisted(t *testing.T) {
	path := t.TempDir()
	for _, directory := range []string{"pending", "failed", "delivered"} {
		if err := os.Mkdir(filepath.Join(path, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	failed := testDelivery()
	failed.Attributes[bridge.FailureNoticeToAttribute] = "sender@example.test"
	now := time.Now().UTC()
	original := item{
		Version: spoolVersion, ID: "0123456789abcdef0123456789abcdef", Delivery: failed,
		State: StateFailed, Attempts: 1, CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	if err := writeItem(filepath.Join(path, "failed", original.ID+".json"), original); err != nil {
		t.Fatal(err)
	}
	notices := make(chan bridge.GatewayDelivery, 2)
	queue, err := New(Config{
		Path: path, Adapters: map[string]bridge.GatewayDeliveryAdapter{"smtp": bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, delivery bridge.GatewayDelivery) error {
			notices <- delivery
			return nil
		})},
		FailureNoticeDomain: "gateway.example.test", PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeQueue(t, queue) })
	select {
	case notice := <-notices:
		if notice.Attributes["mail_from"] != "" || notice.Target != "sender@example.test" {
			t.Fatalf("unsafe recovered notice: %+v", notice)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failed item did not recover its missing notice")
	}
	status, err := queue.Status(original.ID)
	if err != nil || status.FailureNoticeID != failureNoticeID(original.ID) {
		t.Fatalf("failed item did not record notice ID: %+v, %v", status, err)
	}
}
