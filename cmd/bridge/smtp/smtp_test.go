package smtp

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	smtpclient "github.com/emersion/go-smtp"

	bridge "github.com/webong/gateway/cmd/bridge"
)

type plannerFunc func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error)

func (f plannerFunc) PlanEvent(ctx context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
	return f(ctx, event)
}

type recordingExecutor struct {
	mu         sync.Mutex
	deliveries []bridge.GatewayDelivery
}

func (e *recordingExecutor) EnqueueGateway(delivery bridge.GatewayDelivery) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deliveries = append(e.deliveries, delivery)
	return true
}

func (e *recordingExecutor) snapshot() []bridge.GatewayDelivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]bridge.GatewayDelivery(nil), e.deliveries...)
}

func TestServerUsesGoSMTPForTransactionsAndQueuesGatewayDelivery(t *testing.T) {
	executor := &recordingExecutor{}
	var planned bridge.GatewayEvent
	server, err := NewServer(Config{
		Address:        "127.0.0.1:0",
		Hostname:       "smtp.example.test",
		PlannerTimeout: time.Second,
	}, plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		planned = event
		return bridge.GatewayDecision{
			Version:  bridge.ProtocolVersion,
			Protocol: bridge.ProtocolSMTP,
			Action:   bridge.GatewayDeliver,
			Deliveries: []bridge.GatewayDelivery{{
				Protocol: bridge.ProtocolHTTP,
				Target:   "https://subscriber.example.test/mail",
			}},
		}, nil
	}), executor)
	if err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, ErrServerClosed) {
			t.Errorf("shutdown SMTP server: %v", err)
		}
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, ErrServerClosed) {
				t.Errorf("serve SMTP server: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("SMTP server did not stop")
		}
	})

	client, err := smtpclient.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Hello("client.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := client.Mail("sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Rcpt("recipient@example.test", nil); err != nil {
		t.Fatal(err)
	}

	data, err := client.Data()
	if err != nil {
		t.Fatal(err)
	}
	message := "Subject: hello\r\n\r\nmessage body\r\n"
	if _, err := io.WriteString(data, message); err != nil {
		t.Fatal(err)
	}
	if err := data.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Quit(); err != nil {
		t.Fatal(err)
	}

	if planned.Protocol != bridge.ProtocolSMTP || planned.Kind != bridge.EventTransaction || planned.Route != "recipient@example.test" {
		t.Fatalf("unexpected planned event: %+v", planned)
	}
	if planned.Attributes["mail_from"] != "sender@example.test" || string(planned.Payload) != message {
		t.Fatalf("unexpected SMTP event data: %+v", planned)
	}
	deliveries := executor.snapshot()
	if len(deliveries) != 1 || string(deliveries[0].Payload) != message {
		t.Fatalf("expected queued message delivery: %+v", deliveries)
	}
}

func TestServerUsesGoSMTPMessageLimit(t *testing.T) {
	server, err := NewServer(Config{
		Address:        "127.0.0.1:0",
		MaxMessageSize: 4,
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		t.Fatal("planner should not run for an oversized message")
		return bridge.GatewayDecision{}, nil
	}), &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}

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

	client, err := smtpclient.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Hello("client.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := client.Mail("sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Rcpt("recipient@example.test", nil); err != nil {
		t.Fatal(err)
	}
	data, err := client.Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(data, "12345\r\n"); err != nil {
		t.Fatal(err)
	}
	err = data.Close()
	var smtpErr *smtpclient.SMTPError
	if !errors.As(err, &smtpErr) || smtpErr.Code != 552 {
		t.Fatalf("expected SMTP 552 data error, got %v", err)
	}
}
