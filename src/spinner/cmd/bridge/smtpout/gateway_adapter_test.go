package smtpout

import (
	"context"
	"net"
	"testing"
	"time"

	smtpserver "github.com/emersion/go-smtp"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

func TestGatewayAdapterDeliversResolvedSMTPMessage(t *testing.T) {
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
	adapter, err := NewGatewayAdapter(sender)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("Subject: transported\r\n\r\nmessage body\r\n")
	if err := adapter.DeliverGateway(context.Background(), bridge.GatewayDelivery{
		Protocol:   bridge.ProtocolSMTP,
		Target:     "inbox@example.net",
		Attributes: map[string]string{"mail_from": "sender@example.test"},
		Payload:    message,
	}); err != nil {
		t.Fatal(err)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.messages) != 1 || backend.messages[0].recipients[0] != "inbox@example.net" {
		t.Fatalf("unexpected delivered messages: %+v", backend.messages)
	}
}
