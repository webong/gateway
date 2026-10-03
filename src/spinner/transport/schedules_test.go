package transport

import (
	"testing"

	"github.com/webong/gateway/driver/automation"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

type recordingScheduleQueue struct{ delivery bridge.GatewayDelivery }

func (queue *recordingScheduleQueue) EnqueueGateway(delivery bridge.GatewayDelivery) error {
	queue.delivery = delivery
	return nil
}

func TestScheduledAssertionRequiresDurableQueue(t *testing.T) {
	delivery := automation.Delivery{URL: "https://example.test/check", ExpectedStatus: 204}
	if EnqueueScheduledDelivery(bridge.NewRelayExecutor(nil, nil), "run-1", delivery) {
		t.Fatal("asserted callback must not enter a volatile worker queue")
	}
	queue := &recordingScheduleQueue{}
	executor := bridge.NewRelayExecutor(nil, nil, bridge.WithGatewayDeliveryQueue(queue))
	if !EnqueueScheduledDelivery(executor, "run-1", delivery) {
		t.Fatal("durable asserted callback was not accepted")
	}
	if queue.delivery.Attributes["expected_status"] != "204" || queue.delivery.Attributes["event_id"] != "run-1" || queue.delivery.Attributes["method"] != "POST" {
		t.Fatalf("assertion or correlation lost: %+v", queue.delivery)
	}
}

func TestScheduledBodyAssertionRequiresDurableQueue(t *testing.T) {
	delivery := automation.Delivery{URL: "https://example.test/check", ExpectedBodySHA256: "68df022b3ec77ce6a89fca29ee0c1ab83ba51931ee63e3ca5b773ec68b55cbb2"}
	if EnqueueScheduledDelivery(bridge.NewRelayExecutor(nil, nil), "run-2", delivery) {
		t.Fatal("body assertion must not enter a volatile worker queue")
	}
	queue := &recordingScheduleQueue{}
	executor := bridge.NewRelayExecutor(nil, nil, bridge.WithGatewayDeliveryQueue(queue))
	if !EnqueueScheduledDelivery(executor, "run-2", delivery) || queue.delivery.Attributes["expected_body_sha256"] != delivery.ExpectedBodySHA256 {
		t.Fatalf("durable body assertion lost: %+v", queue.delivery)
	}
}
