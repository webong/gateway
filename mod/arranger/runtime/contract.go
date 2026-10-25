// Package runtime defines the process contract used by an Extension's
// Spinner entrypoint. It lets the core host supervise traffic capabilities
// without compiling their implementations.
package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/automation"
	"github.com/webong/gateway/arranger/protocol"
)

const (
	OperationStart = "start"

	OperationPlanHTTP    = "plan-http"
	OperationPlanEvent   = "plan-event"
	OperationPassThrough = "pass-through"

	OperationDeliver        = "deliver"
	OperationEnqueue        = "enqueue"
	OperationEnqueueGateway = "enqueue-gateway"
	OperationDurable        = "durable"

	OperationExecuteAutomation = "execute"
	OperationRegisterSchedules = "register"
)

var (
	Contract           = plugin.ContractRef{Name: "gateway.runtime", Version: "v1"}
	PlannerContract    = plugin.ContractRef{Name: "gateway.planner", Version: "v1"}
	DeliveryContract   = plugin.ContractRef{Name: "gateway.delivery", Version: "v1"}
	AutomationContract = plugin.ContractRef{Name: "gateway.automation-host", Version: "v1"}
	ScheduleContract   = plugin.ContractRef{Name: "gateway.schedule", Version: "v1"}
)

// StartRequest contains host-owned ceilings. Product routing and policy stay
// in Planner; the process receives no Planner URL or internal credential.
type StartRequest struct {
	Configuration           json.RawMessage       `json:"configuration,omitempty"`
	MaxBodySize             int64                 `json:"maxBodySize"`
	PlannerTimeout          time.Duration         `json:"plannerTimeout"`
	WebSocketMaxConnections int                   `json:"webSocketMaxConnections"`
	WebSocketMaxDuration    time.Duration         `json:"webSocketMaxDuration"`
	WebstreamHeartbeat      time.Duration         `json:"webstreamHeartbeat"`
	WebstreamRetry          time.Duration         `json:"webstreamRetry"`
	WebstreamMaxConnections int                   `json:"webstreamMaxConnections"`
	WebstreamMaxDuration    time.Duration         `json:"webstreamMaxDuration"`
	AutomationLanguages     []automation.Language `json:"automationLanguages,omitempty"`
}

// HTTPMount advertises one private HTTP endpoint and the public path prefix it
// owns. Address must be an http loopback URL; it is never exposed directly.
type HTTPMount struct {
	Prefix  string `json:"prefix"`
	Address string `json:"address"`
}

// StartResponse advertises data endpoints opened by the extension process.
// Multiple Extensions can coexist because HTTP ownership is prefix-scoped.
type StartResponse struct {
	HTTP []HTTPMount `json:"http,omitempty"`
}

type HTTPRequest struct {
	Method     string      `json:"method"`
	Scheme     string      `json:"scheme,omitempty"`
	Host       string      `json:"host"`
	Path       string      `json:"path"`
	RawQuery   string      `json:"rawQuery,omitempty"`
	Headers    http.Header `json:"headers,omitempty"`
	Body       []byte      `json:"body,omitempty"`
	RemoteAddr string      `json:"remoteAddr,omitempty"`
}

type HTTPResponse struct {
	Status  int         `json:"status"`
	Headers http.Header `json:"headers,omitempty"`
	Body    []byte      `json:"body,omitempty"`
}

type AutomationRequest struct {
	Script automation.Script `json:"script"`
	Input  automation.Input  `json:"input"`
}

type AutomationResponse struct {
	Outcome automation.Outcome `json:"outcome"`
}

type ScheduleRequest struct {
	Origin    string                `json:"origin"`
	Schedules []automation.Schedule `json:"schedules"`
}

type DeliveryResponse struct {
	Response protocol.Response `json:"response"`
}

type EnqueueResponse struct {
	Accepted bool `json:"accepted"`
}

type DurableResponse struct {
	Enabled bool `json:"enabled"`
}

// Starter opens extension-owned listeners after the host has verified and
// launched the selected package revision.
type Starter interface {
	Start(context.Context, StartRequest, plugin.ReverseCaller) (StartResponse, error)
}

type StarterFunc func(context.Context, StartRequest, plugin.ReverseCaller) (StartResponse, error)

func (f StarterFunc) Start(ctx context.Context, request StartRequest, caller plugin.ReverseCaller) (StartResponse, error) {
	return f(ctx, request, caller)
}

func marshal(value any) (json.RawMessage, error) { return json.Marshal(value) }
