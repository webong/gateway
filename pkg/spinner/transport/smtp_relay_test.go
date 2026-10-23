package transport

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	smtpserver "github.com/emersion/go-smtp"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	relayconfig "github.com/webong/gateway/pkg/spinner/internal/config"
)

type bundledSMTPPlanner struct{}

func (bundledSMTPPlanner) PlanEvent(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
	if event.Kind != bridge.EventTransaction {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayReject}, nil
	}
	return bridge.GatewayDecision{
		Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewaySubmit,
		Deliveries: []bridge.GatewayDelivery{{
			Protocol: bridge.ProtocolSMTP, Target: "inbox@example.net",
			Attributes: map[string]string{"mail_from": event.Attributes["mail_from"]},
		}},
	}, nil
}

type unusedSMTPExecutor struct{}

func (unusedSMTPExecutor) EnqueueGateway(bridge.GatewayDelivery) bool { return false }

type relayRecordingBackend struct{ accepted chan []byte }

func (backend *relayRecordingBackend) NewSession(*smtpserver.Conn) (smtpserver.Session, error) {
	return &relayRecordingSession{backend: backend}, nil
}

type relayRecordingSession struct {
	backend *relayRecordingBackend
	from    string
	to      string
}

func (*relayRecordingSession) AuthMechanisms() []string { return nil }
func (*relayRecordingSession) Auth(string) (sasl.Server, error) {
	return nil, smtpserver.ErrAuthUnsupported
}
func (s *relayRecordingSession) Mail(from string, _ *smtpserver.MailOptions) error {
	s.from = from
	return nil
}
func (s *relayRecordingSession) Rcpt(to string, _ *smtpserver.RcptOptions) error {
	s.to = to
	return nil
}
func (s *relayRecordingSession) Data(reader io.Reader) error {
	data, err := io.ReadAll(reader)
	if err == nil && s.from == "sender@example.test" && s.to == "inbox@example.net" {
		s.backend.accepted <- data
	}
	return err
}
func (s *relayRecordingSession) Reset()      { s.from = ""; s.to = "" }
func (*relayRecordingSession) Logout() error { return nil }

func TestBundledSMTPListenerSubmitsToConfiguredRelay(t *testing.T) {
	backend := &relayRecordingBackend{accepted: make(chan []byte, 1)}
	relay := smtpserver.NewServer(backend)
	relayListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	relayDone := make(chan error, 1)
	go func() { relayDone <- relay.Serve(relayListener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = relay.Shutdown(ctx)
		<-relayDone
	})

	server, err := newSMTPServer(&relayconfig.Config{
		SMTPAddress: "127.0.0.1:0", SMTPHostname: "gateway.example.test",
		SMTPRelayAddress: relayListener.Addr().String(), SMTPRelayTLSMode: "none",
		InternalToken: "test-internal-token",
	}, bundledSMTPPlanner{}, unusedSMTPExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-serveDone
	})

	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client := smtpserver.NewClient(connection)
	defer client.Close()
	if err := client.Hello("sender.example.test"); err != nil {
		t.Fatal(err)
	}
	message := []byte("From: sender@example.test\r\nSubject: bundled relay\r\n\r\nbody\r\n")
	if err := client.SendMail("sender@example.test", []string{"inbox@example.net"}, bytes.NewReader(message)); err != nil {
		t.Fatal(err)
	}
	select {
	case delivered := <-backend.accepted:
		if !strings.Contains(string(delivered), "Subject: bundled relay\r\n") {
			t.Fatalf("relay received the wrong message: %s", delivered)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bundled SMTP listener did not reach its configured relay")
	}
}
