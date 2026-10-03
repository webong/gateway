package transport

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"github.com/webong/gateway/driver/automation"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/internal/automation/schedules"
)

// ScheduleControl is the shared private schedule protocol for an embedded
// Planner. Router compositions do not import Spinner's internal package.
type ScheduleControl = schedules.HTTPControlPlane

func EmbeddedScheduleControl(handler http.Handler, spinner *Router, executor automation.Executor) (*ScheduleControl, error) {
	return schedules.NewHandlerControlPlane(handler, spinner.InternalToken, automation.Languages(executor)...)
}

func StartSchedulePolling(ctx context.Context, spinner *Router, control *ScheduleControl, executor automation.Executor) {
	(schedules.Runtime{
		Control: control, Executor: executor, Registrar: control,
		Interval: spinner.SchedulePollInterval,
		LogError: func(format string, args ...any) { log.Printf(format, args...) },
		Enqueue: func(runID string, delivery automation.Delivery) bool {
			return EnqueueScheduledDelivery(spinner.Executor, runID, delivery)
		},
	}).Start(ctx)
}

// EnqueueScheduledDelivery preserves the legacy callback path unless a status
// assertion requires the durable spool and its observable receipt.
func EnqueueScheduledDelivery(executor *bridge.RelayExecutor, runID string, delivery automation.Delivery) bool {
	if delivery.ExpectedStatus == 0 && delivery.ExpectedBodySHA256 == "" {
		return executor.Enqueue(bridge.Delivery{
			URL: delivery.URL, EventID: runID, Method: delivery.Method,
			Headers: delivery.Headers, Body: []byte(delivery.Body),
		})
	}
	if !executor.DurableGatewayDeliveries() {
		return false
	}
	method := delivery.Method
	if method == "" {
		method = http.MethodPost
	}
	attributes := map[string]string{"method": method, "event_id": runID}
	if delivery.ExpectedStatus != 0 {
		attributes["expected_status"] = strconv.Itoa(delivery.ExpectedStatus)
	}
	if delivery.ExpectedBodySHA256 != "" {
		attributes["expected_body_sha256"] = delivery.ExpectedBodySHA256
	}
	return executor.EnqueueGateway(bridge.GatewayDelivery{
		Protocol: bridge.ProtocolHTTP, Target: delivery.URL,
		Headers: delivery.Headers, Payload: []byte(delivery.Body),
		Attributes: attributes,
	})
}
