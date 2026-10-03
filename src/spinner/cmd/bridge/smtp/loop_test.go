package smtp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	smtpclient "github.com/emersion/go-smtp"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

func TestForwardingMarkersAreSignedAndBounded(t *testing.T) {
	key := []byte("gateway-test-loop-key")
	message := []byte("Subject: test\r\n\r\nbody\r\n")
	for hop := 1; hop <= maxForwardingHops; hop++ {
		var err error
		message, err = markForwarding(message, key)
		if err != nil || forwardingHops(message, key) != hop {
			t.Fatalf("forwarding hop %d was not signed: %v", hop, err)
		}
	}
	if forwardingHops(message, []byte("different-key")) != 0 {
		t.Fatal("accepted a forwarding marker signed by another deployment")
	}
	forged := append([]byte("X-Gateway-Forwarded: v1.forged.invalid\r\n"), message...)
	if forwardingHops(forged, key) != maxForwardingHops {
		t.Fatal("counted an unsigned forwarding marker")
	}
}

func TestSMTPSubmitRejectsForwardingLoopsWithoutCallingRelay(t *testing.T) {
	key := []byte("gateway-test-loop-key")
	message := []byte("Subject: test\r\n\r\nbody\r\n")
	for range maxForwardingHops {
		var err error
		message, err = markForwarding(message, key)
		if err != nil {
			t.Fatal(err)
		}
	}
	called := false
	s := &session{
		planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			return bridge.GatewayDecision{
				Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewaySubmit,
				Deliveries: []bridge.GatewayDelivery{{Protocol: bridge.ProtocolSMTP, Target: "target@example.net"}},
			}, nil
		}),
		executor: &recordingExecutor{},
		config: Config{MaxMessageSize: 4096, PlannerTimeout: time.Second, SubmitTimeout: time.Second,
			Submitter: bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
				called = true
				return nil
			}), LoopKey: key},
		from: "sender@example.test", recipients: []string{"alias@example.test"},
	}
	err := s.Data(strings.NewReader(string(message)))
	var smtpErr *smtpclient.SMTPError
	if !errors.As(err, &smtpErr) || smtpErr.Code != 554 || called {
		t.Fatalf("forwarding loop reached relay: err=%v called=%v", err, called)
	}

	received := strings.Repeat("Received: from remote.example.test\r\n", maxReceivedHops) + "Subject: test\r\n\r\nbody\r\n"
	err = s.Data(strings.NewReader(received))
	if !errors.As(err, &smtpErr) || smtpErr.Code != 554 || called {
		t.Fatalf("excessive Received trace reached relay: err=%v called=%v", err, called)
	}
}

func TestSMTPServerRequiresForwardingKeyForSubmission(t *testing.T) {
	_, err := NewServer(Config{
		Address: "127.0.0.1:2525",
		Submitter: bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
			return nil
		}),
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{}, nil
	}), &recordingExecutor{})
	if err == nil || !strings.Contains(err.Error(), "forwarding-loop key") {
		t.Fatalf("SMTP submission started without a loop key: %v", err)
	}
}
