package smtpout

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	smtpserver "github.com/emersion/go-smtp"
	bridge "github.com/webong/gateway/arranger/integration"
	emailattrs "github.com/webong/gateway/pkg/email/spinner/attributes"
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
	adapter, err := NewGatewayAdapter(sender, nil)
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
	if err := adapter.DeliverGateway(context.Background(), bridge.GatewayDelivery{
		Protocol: bridge.ProtocolSMTP, Target: "first@example.net",
		Attributes: map[string]string{
			"mail_from":  "sender@example.test",
			"recipients": `["first@example.net","second@example.net"]`,
		},
		Payload: message,
	}); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signingAdapter, err := NewGatewayAdapter(sender, &DKIMSigner{keys: map[string]dkimSigningKey{
		"example.test": {selector: "test", signer: privateKey},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := signingAdapter.DeliverGateway(context.Background(), bridge.GatewayDelivery{
		Protocol: bridge.ProtocolSMTP, Target: "signed@example.net",
		Attributes: map[string]string{
			"mail_from": "sender@example.test", emailattrs.DKIMSigningDomain: "example.test",
		},
		Payload: []byte("From: Sender <sender@example.test>\r\nSubject: signed\r\n\r\nbody\r\n"),
	}); err != nil {
		t.Fatal(err)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.messages) != 3 || backend.messages[0].recipients[0] != "inbox@example.net" ||
		len(backend.messages[1].recipients) != 2 || backend.messages[1].recipients[0] != "first@example.net" || backend.messages[1].recipients[1] != "second@example.net" {
		t.Fatalf("unexpected delivered messages: %+v", backend.messages)
	}
	verified, err := dkim.VerifyWithOptions(bytes.NewReader(backend.messages[2].data), &dkim.VerifyOptions{LookupTXT: func(domain string) ([]string, error) {
		if domain != "test._domainkey.example.test" {
			t.Fatalf("unexpected DKIM lookup: %s", domain)
		}
		return []string{"v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(publicKey)}, nil
	}})
	if err != nil || len(verified) != 1 || verified[0].Err != nil {
		t.Fatalf("outbound SMTP relay did not receive a valid DKIM signature: %+v, %v", verified, err)
	}
}

func TestGatewayAdapterRejectsInvalidRecipientBatch(t *testing.T) {
	adapter, err := NewGatewayAdapter(&Sender{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.DeliverGateway(context.Background(), bridge.GatewayDelivery{
		Protocol: bridge.ProtocolSMTP, Target: "first@example.net",
		Attributes: map[string]string{"recipients": `["other@example.net"]`},
		Payload:    []byte("Subject: test\r\n\r\nbody"),
	}); err == nil {
		t.Fatal("inconsistent recipient batch was accepted")
	}
}
