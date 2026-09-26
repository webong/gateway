package smtpout

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	smtpserver "github.com/emersion/go-smtp"
)

type recordedMessage struct {
	from       string
	recipients []string
	data       []byte
}

type recordingBackend struct {
	mu       sync.Mutex
	messages []recordedMessage
}

func (backend *recordingBackend) NewSession(*smtpserver.Conn) (smtpserver.Session, error) {
	return &recordingSession{backend: backend}, nil
}

type recordingSession struct {
	backend    *recordingBackend
	from       string
	recipients []string
}

func (*recordingSession) AuthMechanisms() []string { return nil }
func (*recordingSession) Auth(string) (sasl.Server, error) {
	return nil, smtpserver.ErrAuthUnsupported
}
func (session *recordingSession) Mail(from string, _ *smtpserver.MailOptions) error {
	session.from = from
	return nil
}
func (session *recordingSession) Rcpt(to string, _ *smtpserver.RcptOptions) error {
	session.recipients = append(session.recipients, to)
	return nil
}
func (session *recordingSession) Data(reader io.Reader) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	session.backend.mu.Lock()
	session.backend.messages = append(session.backend.messages, recordedMessage{
		from:       session.from,
		recipients: append([]string(nil), session.recipients...),
		data:       data,
	})
	session.backend.mu.Unlock()
	return nil
}
func (session *recordingSession) Reset() {
	session.from = ""
	session.recipients = nil
}
func (session *recordingSession) Logout() error { return nil }

func TestSenderSubmitsUnchangedMessageToConfiguredRelay(t *testing.T) {
	backend := &recordingBackend{}
	server := smtpserver.NewServer(backend)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-serveErr
	})

	sender, err := NewSender(Config{
		Address:   listener.Addr().String(),
		LocalName: "gateway.example.test",
		TLSMode:   TLSNone,
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("From: sender@example.test\r\nTo: inbox@example.net\r\nSubject: hello\r\n\r\nmessage body\r\n")
	if err := sender.Send(context.Background(), Message{
		EnvelopeFrom: "sender@example.test",
		Recipients:   []string{"inbox@example.net"},
		Data:         message,
	}); err != nil {
		t.Fatal(err)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.messages) != 1 {
		t.Fatalf("expected one message, got %d", len(backend.messages))
	}
	received := backend.messages[0]
	if received.from != "sender@example.test" || len(received.recipients) != 1 || received.recipients[0] != "inbox@example.net" {
		t.Fatalf("unexpected envelope: %+v", received)
	}
	if string(received.data) != string(message) {
		t.Fatalf("message changed in transit: %q", received.data)
	}
}

func TestSenderRejectsIncompleteCredentials(t *testing.T) {
	_, err := NewSender(Config{Address: "smtp.example.test:587", Username: "gateway"})
	if err == nil || err.Error() != "outbound SMTP username and password must be configured together" {
		t.Fatalf("expected credential validation error, got %v", err)
	}
}

func TestSenderRejectsAuthenticationWithoutTLS(t *testing.T) {
	_, err := NewSender(Config{
		Address:  "smtp.example.test:25",
		Username: "gateway",
		Password: "secret",
		TLSMode:  TLSNone,
	})
	if err == nil || err.Error() != "outbound SMTP authentication requires TLS" {
		t.Fatalf("expected TLS validation error, got %v", err)
	}
}
