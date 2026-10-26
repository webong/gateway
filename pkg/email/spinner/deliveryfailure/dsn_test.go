package deliveryfailure

import (
	"context"
	"strings"
	"testing"
	"time"

	protocol "github.com/webong/gateway/arranger/integration"
	emailattrs "github.com/webong/gateway/pkg/email/spinner/attributes"
)

func TestEmailBuildsItsOwnFailureNotice(t *testing.T) {
	handler, err := New("gateway.example.test")
	if err != nil {
		t.Fatal(err)
	}
	followUp, err := handler.FollowUp(context.Background(), protocol.DeliveryFailureRequest{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", EventID: "event-1", Attempts: 3,
		CreatedAt: time.Unix(10, 0), FailedAt: time.Unix(20, 0),
		Delivery: protocol.GatewayDelivery{
			Protocol: protocol.ProtocolSMTP, Adapter: "managed.smtp", Target: "buyer@example.test",
			Attributes: map[string]string{
				"mail_from": "sender@example.test", emailattrs.FailureNoticeTo: "sender@example.test",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if followUp == nil || followUp.Adapter != "smtp" || followUp.Target != "sender@example.test" ||
		followUp.Attributes["mail_from"] != "" || !strings.Contains(string(followUp.Payload), "Delivery Status Notification") {
		t.Fatalf("unexpected Email failure notice: %+v", followUp)
	}
}

func TestEmailFailureNoticeFailsClosedForUntrustedSender(t *testing.T) {
	handler, err := New("gateway.example.test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.FollowUp(context.Background(), protocol.DeliveryFailureRequest{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: time.Now(), FailedAt: time.Now(),
		Delivery: protocol.GatewayDelivery{
			Protocol: protocol.ProtocolSMTP, Adapter: "smtp", Target: "buyer@example.test",
			Attributes: map[string]string{
				"mail_from": "sender@example.test", emailattrs.FailureNoticeTo: "attacker@example.test",
			},
		},
	})
	if err == nil {
		t.Fatal("untrusted failure notice sender was accepted")
	}
}

func TestEmailFailureHandlerIgnoresDeliveriesWithoutNoticeApproval(t *testing.T) {
	handler, err := New("gateway.example.test")
	if err != nil {
		t.Fatal(err)
	}
	followUp, err := handler.FollowUp(context.Background(), protocol.DeliveryFailureRequest{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: time.Now(), FailedAt: time.Now(),
		Delivery: protocol.GatewayDelivery{Protocol: protocol.ProtocolSMTP, Adapter: "smtp", Target: "buyer@example.test"},
	})
	if err != nil || followUp != nil {
		t.Fatalf("unapproved delivery produced a notice: %+v %v", followUp, err)
	}
}
