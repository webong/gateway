package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/automation"
)

const (
	OperationStart = "start"

	OperationPlanHTTP           = "plan-http"
	OperationPlanEvent          = "plan-event"
	OperationPassThrough        = "pass-through"
	OperationPrivatePassThrough = "private-pass-through"

	OperationDeliver                  = "deliver"
	OperationEnqueue                  = "enqueue"
	OperationEnqueueGateway           = "enqueue-gateway"
	OperationEnqueueGatewayIdempotent = "enqueue-gateway-idempotent"
	OperationSupportsDeliveryAdapter  = "supports-delivery-adapter"
	OperationDurable                  = "durable"

	OperationExecuteAutomation = "execute"
	OperationRegisterSchedules = "register"
	OperationRecordObservation = "record"
	OperationResolveSecret     = "resolve"
	OperationDeliverAdapter    = "deliver"
	OperationRunJob            = "run"
	OperationDeliveryFailure   = "follow-up"
)

var (
	RuntimeContract         = plugin.ContractRef{Name: "gateway.runtime", Version: "v1"}
	PlannerContract         = plugin.ContractRef{Name: "gateway.planner", Version: "v1"}
	DeliveryContract        = plugin.ContractRef{Name: "gateway.delivery", Version: "v1"}
	AutomationContract      = plugin.ContractRef{Name: "gateway.automation-host", Version: "v1"}
	ScheduleContract        = plugin.ContractRef{Name: "gateway.schedule", Version: "v1"}
	ObservationContract     = plugin.ContractRef{Name: "gateway.observation", Version: "v1"}
	SecretContract          = plugin.ContractRef{Name: "gateway.secret", Version: "v1"}
	DeliveryAdapterContract = plugin.ContractRef{Name: "gateway.delivery-adapter", Version: "v1"}
	JobContract             = plugin.ContractRef{Name: "gateway.job", Version: "v1"}
	DeliveryFailureContract = plugin.ContractRef{Name: "gateway.delivery-failure", Version: "v1"}
)

// RuntimeStartRequest contains host-owned ceilings. Product routing and policy stay
// in Planner; the process receives no Planner URL or internal credential.
type RuntimeStartRequest struct {
	Configuration       json.RawMessage       `json:"configuration,omitempty"`
	MaxBodySize         int64                 `json:"maxBodySize"`
	PlannerTimeout      time.Duration         `json:"plannerTimeout"`
	AutomationLanguages []automation.Language `json:"automationLanguages,omitempty"`
	// InstanceKey is an opaque, identity-scoped host key. Extensions may use it
	// for local integrity tags without receiving the host's internal credential.
	InstanceKey []byte `json:"instanceKey,omitempty"`
}

// HTTPMount advertises one private HTTP endpoint and the public path prefix it
// owns. Address must be an http loopback URL; it is never exposed directly.
type HTTPMount struct {
	Prefix  string `json:"prefix"`
	Address string `json:"address"`
}

// RuntimeStartResponse advertises data endpoints opened by the extension process.
// Multiple Extensions can coexist because HTTP ownership is prefix-scoped.
type RuntimeStartResponse struct {
	HTTP                    []HTTPMount  `json:"http,omitempty"`
	PrivateHTTP             []HTTPMount  `json:"privateHttp,omitempty"`
	DeliveryAdapters        []string     `json:"deliveryAdapters,omitempty"`
	Jobs                    []RuntimeJob `json:"jobs,omitempty"`
	DeliveryFailureHandlers []string     `json:"deliveryFailureHandlers,omitempty"`
}

type RuntimeJob struct {
	Name     string              `json:"name"`
	Language automation.Language `json:"language"`
}

type SecretRequest struct {
	Reference string `json:"reference"`
}

type SecretValue struct {
	Reference string          `json:"reference"`
	Version   int64           `json:"version"`
	Value     json.RawMessage `json:"value"`
}

type SecretResolver interface {
	Resolve(context.Context, string) (SecretValue, error)
}

type JobRequest struct {
	Name    string         `json:"name"`
	Source  string         `json:"source"`
	Payload map[string]any `json:"payload,omitempty"`
}

type DeliveryFailureRequest struct {
	ID        string          `json:"id"`
	EventID   string          `json:"eventId,omitempty"`
	Delivery  GatewayDelivery `json:"delivery"`
	Attempts  int             `json:"attempts"`
	CreatedAt time.Time       `json:"createdAt"`
	FailedAt  time.Time       `json:"failedAt"`
}

type DeliveryFailureResponse struct {
	FollowUp *GatewayDelivery `json:"followUp,omitempty"`
}

type DeliveryFailureHandler interface {
	FollowUp(context.Context, DeliveryFailureRequest) (*GatewayDelivery, error)
}

type AdapterFailure string

const (
	AdapterFailureTransient AdapterFailure = "transient"
	AdapterFailurePermanent AdapterFailure = "permanent"
	AdapterFailureUncertain AdapterFailure = "uncertain"
)

type DeliveryAdapterRequest struct {
	Adapter  string          `json:"adapter"`
	Delivery GatewayDelivery `json:"delivery"`
}

type DeliveryAdapterResponse struct {
	Result  GatewayDeliveryResult `json:"result"`
	Failure AdapterFailure        `json:"failure,omitempty"`
}

type PassThroughRequest struct {
	Method     string      `json:"method"`
	Scheme     string      `json:"scheme,omitempty"`
	Host       string      `json:"host"`
	Path       string      `json:"path"`
	RawQuery   string      `json:"rawQuery,omitempty"`
	Headers    http.Header `json:"headers,omitempty"`
	Body       []byte      `json:"body,omitempty"`
	RemoteAddr string      `json:"remoteAddr,omitempty"`
}

type PassThroughResponse struct {
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
	Response Response `json:"response"`
}

type EnqueueResponse struct {
	Accepted bool `json:"accepted"`
}

type IdempotentEnqueueRequest struct {
	Delivery    GatewayDelivery `json:"delivery"`
	ID          string          `json:"id"`
	Fingerprint string          `json:"fingerprint"`
	ExpiresAt   time.Time       `json:"expiresAt"`
}

type IdempotentEnqueueResponse struct {
	Replayed bool `json:"replayed"`
	Conflict bool `json:"conflict,omitempty"`
}

type DeliveryAdapterSupportRequest struct {
	Adapter string `json:"adapter"`
}

type DeliveryAdapterSupportResponse struct {
	Supported bool `json:"supported"`
}

type DurableResponse struct {
	Enabled bool `json:"enabled"`
}

// RuntimeStarter opens extension-owned listeners after the host has verified and
// launched the selected package revision.
type RuntimeStarter interface {
	Start(context.Context, RuntimeStartRequest, plugin.ReverseCaller) (RuntimeStartResponse, error)
}

type RuntimeStarterFunc func(context.Context, RuntimeStartRequest, plugin.ReverseCaller) (RuntimeStartResponse, error)

func (f RuntimeStarterFunc) Start(ctx context.Context, request RuntimeStartRequest, caller plugin.ReverseCaller) (RuntimeStartResponse, error) {
	return f(ctx, request, caller)
}

func marshal(value any) (json.RawMessage, error) { return json.Marshal(value) }
