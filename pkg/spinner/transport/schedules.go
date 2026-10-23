package transport

import (
	"context"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/webong/gateway/driver/automation"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/automation/schedules"
	"github.com/webong/gateway/pkg/spinner/internal/forwarding"
	"github.com/webong/gateway/pkg/spinner/internal/mailpoll"
)

// ScheduleControl is the shared private schedule protocol for an embedded
// Planner. Router compositions do not import Spinner's internal package.
type ScheduleControl = schedules.HTTPControlPlane

func EmbeddedScheduleControl(handler http.Handler, spinner *Router, executor automation.Executor) (*ScheduleControl, error) {
	return schedules.NewHandlerControlPlane(handler, spinner.InternalToken, automation.Languages(executor)...)
}

func StartSchedulePolling(ctx context.Context, spinner *Router, control *ScheduleControl, executor automation.Executor,
	nativeIMAP func(context.Context, string) error) {
	(schedules.Runtime{
		Control: control, Executor: executor, Registrar: control, NativeIMAP: nativeIMAP,
		Interval: spinner.SchedulePollInterval,
		LogError: func(format string, args ...any) { log.Printf(format, args...) },
		Enqueue: func(runID string, delivery automation.Delivery) bool {
			return EnqueueScheduledDelivery(spinner.Executor, runID, delivery)
		},
	}).Start(ctx)
}

func EmbeddedMailboxPoller(handler http.Handler, spinner *Router) (mailpoll.Runner, error) {
	control, err := mailpoll.NewHandlerControlPlane(handler, spinner.InternalToken)
	if err != nil {
		return mailpoll.Runner{}, err
	}
	return mailpoll.Runner{Control: control,
		Dial: func(ctx context.Context, claim mailpoll.Claim) (net.Conn, error) {
			return bridge.DialTLSMailUpstream(ctx, claim.UpstreamAddress, claim.UpstreamServerName,
				claim.UpstreamNetworkScope, 10*time.Second, nil)
		},
		Deliver: func(ctx context.Context, target string, headers http.Header, body []byte) (int, error) {
			return spinner.forwarder.ForwardStatus(forwarding.ForwardRequest{
				TargetURL: target, Method: http.MethodPost, Headers: headers, RequestBody: body, Context: ctx,
			})
		},
	}, nil
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
