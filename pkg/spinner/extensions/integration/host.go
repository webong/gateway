// Package integrationextensions hosts installable Spinner integration
// entrypoints. It depends only on Arranger and ext contracts; product
// extensions remain independently built processes.
package integrationextensions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/automation"
	"github.com/webong/gateway/arranger/integration"
)

type Executor interface {
	integration.Executor
	integration.GatewayExecutor
}

// Services are the host capabilities an installed integration may call. The
// descriptor still has to declare every exact reverse operation.
type Services struct {
	Planner         integration.Planner
	ProtocolPlanner integration.ProtocolPlanner
	PassThrough     http.Handler
	Executor        Executor
	Automation      automation.Executor
	Schedules       automation.ScheduleRegistrar
	Observations    integration.ObservationSink
	Start           integration.RuntimeStartRequest
}

// Host is late-bound so a package can be launched before an embedded Planner
// finishes booting. Calls fail closed until Configure installs live services.
type Host struct {
	mu       sync.RWMutex
	services Services
}

func NewHost() *Host { return &Host{} }

var DefaultHost = NewHost()

func (h *Host) Configure(services Services) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.services = services
}

func (h *Host) current() Services {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.services
}

func (h *Host) StartRequest(configuration json.RawMessage) integration.RuntimeStartRequest {
	request := h.current().Start
	request.Configuration = append(json.RawMessage(nil), configuration...)
	return request
}

// ReverseOptions combines the integration host services with an optional existing
// reverse host such as Companion Actions.
func (h *Host) ReverseOptions(fallback *plugin.ReverseOptions) *plugin.ReverseOptions {
	return &plugin.ReverseOptions{
		Authorize: func(ctx context.Context, request plugin.Request) error {
			if h.owns(request.Contract) {
				return h.authorize(request)
			}
			if fallback == nil || fallback.Authorize == nil {
				return plugin.ErrDenied
			}
			return fallback.Authorize(ctx, request)
		},
		Handle: func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
			if h.owns(request.Contract) {
				return h.handle(ctx, request)
			}
			if fallback == nil || fallback.Handle == nil {
				return nil, plugin.ErrDenied
			}
			return fallback.Handle(ctx, request)
		},
		Timeout:         5 * time.Minute,
		MaxPayloadBytes: plugin.MaxFrameBytes - (64 << 10),
		MaxInFlight:     16,
	}
}

func (h *Host) owns(ref plugin.ContractRef) bool {
	return ref == integration.PlannerContract || ref == integration.DeliveryContract ||
		ref == integration.AutomationContract || ref == integration.ScheduleContract ||
		ref == integration.ObservationContract
}

func (h *Host) authorize(request plugin.Request) error {
	services := h.current()
	switch request.Contract {
	case integration.PlannerContract:
		if request.Operation == integration.OperationPlanHTTP && services.Planner != nil ||
			request.Operation == integration.OperationPlanEvent && services.ProtocolPlanner != nil ||
			request.Operation == integration.OperationPassThrough && services.PassThrough != nil {
			return nil
		}
	case integration.DeliveryContract:
		if services.Executor != nil && (request.Operation == integration.OperationDeliver ||
			request.Operation == integration.OperationEnqueue || request.Operation == integration.OperationEnqueueGateway ||
			request.Operation == integration.OperationDurable) {
			return nil
		}
	case integration.AutomationContract:
		if services.Automation != nil && request.Operation == integration.OperationExecuteAutomation {
			return nil
		}
	case integration.ScheduleContract:
		if services.Schedules != nil && request.Operation == integration.OperationRegisterSchedules {
			return nil
		}
	case integration.ObservationContract:
		if services.Observations != nil && request.Operation == integration.OperationRecordObservation {
			return nil
		}
	}
	return plugin.ErrDenied
}

func (h *Host) handle(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
	services := h.current()
	switch request.Contract {
	case integration.PlannerContract:
		return handlePlanner(ctx, services, request)
	case integration.DeliveryContract:
		return handleDelivery(ctx, services, request)
	case integration.AutomationContract:
		var input integration.AutomationRequest
		if err := decode(request.Payload, &input); err != nil {
			return nil, plugin.ErrInvalid
		}
		outcome, err := services.Automation.Execute(ctx, input.Script, input.Input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(integration.AutomationResponse{Outcome: outcome})
	case integration.ScheduleContract:
		var input integration.ScheduleRequest
		if err := decode(request.Payload, &input); err != nil || strings.TrimSpace(input.Origin) == "" {
			return nil, plugin.ErrInvalid
		}
		if err := services.Schedules.RegisterSchedules(ctx, input.Origin, input.Schedules); err != nil {
			return nil, err
		}
		return json.RawMessage("null"), nil
	case integration.ObservationContract:
		var input integration.Observation
		if err := decode(request.Payload, &input); err != nil || input.Validate() != nil {
			return nil, plugin.ErrInvalid
		}
		if err := services.Observations.RecordObservation(ctx, input); err != nil {
			return nil, err
		}
		return json.RawMessage("null"), nil
	default:
		return nil, plugin.ErrUnsupported
	}
}

func handlePlanner(ctx context.Context, services Services, request plugin.Request) (json.RawMessage, error) {
	switch request.Operation {
	case integration.OperationPlanHTTP:
		var input integration.IngressRequest
		if err := decode(request.Payload, &input); err != nil {
			return nil, plugin.ErrInvalid
		}
		result, err := services.Planner.Plan(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case integration.OperationPlanEvent:
		var input integration.GatewayEvent
		if err := decode(request.Payload, &input); err != nil {
			return nil, plugin.ErrInvalid
		}
		result, err := services.ProtocolPlanner.PlanEvent(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case integration.OperationPassThrough:
		var input integration.PassThroughRequest
		if err := decode(request.Payload, &input); err != nil || input.Method == "" || input.Path == "" || pathUnder(input.Path, "/_internal") {
			return nil, plugin.ErrInvalid
		}
		target := input.Scheme + "://" + input.Host + input.Path
		if input.Scheme == "" {
			target = "http://" + input.Host + input.Path
		}
		if input.RawQuery != "" {
			target += "?" + input.RawQuery
		}
		req, err := http.NewRequestWithContext(ctx, input.Method, target, bytes.NewReader(input.Body))
		if err != nil {
			return nil, plugin.ErrInvalid
		}
		req.Header = input.Headers.Clone()
		req.Host = input.Host
		req.RemoteAddr = input.RemoteAddr
		recorder := httptest.NewRecorder()
		services.PassThrough.ServeHTTP(recorder, req)
		response := recorder.Result()
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, plugin.MaxFrameBytes))
		if err != nil {
			return nil, err
		}
		return json.Marshal(integration.PassThroughResponse{Status: response.StatusCode, Headers: response.Header.Clone(), Body: body})
	default:
		return nil, plugin.ErrUnsupported
	}
}

func handleDelivery(ctx context.Context, services Services, request plugin.Request) (json.RawMessage, error) {
	switch request.Operation {
	case integration.OperationDeliver:
		var input integration.Delivery
		if err := decode(request.Payload, &input); err != nil || input.Validate() != nil {
			return nil, plugin.ErrInvalid
		}
		response, err := services.Executor.Deliver(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(integration.DeliveryResponse{Response: response})
	case integration.OperationEnqueue:
		var input integration.Delivery
		if err := decode(request.Payload, &input); err != nil || input.Validate() != nil {
			return nil, plugin.ErrInvalid
		}
		return json.Marshal(integration.EnqueueResponse{Accepted: services.Executor.Enqueue(input)})
	case integration.OperationEnqueueGateway:
		var input integration.GatewayDelivery
		if err := decode(request.Payload, &input); err != nil || input.Validate() != nil {
			return nil, plugin.ErrInvalid
		}
		return json.Marshal(integration.EnqueueResponse{Accepted: services.Executor.EnqueueGateway(input)})
	case integration.OperationDurable:
		durable, ok := services.Executor.(integration.DurableGatewayExecutor)
		return json.Marshal(integration.DurableResponse{Enabled: ok && durable.DurableGatewayDeliveries()})
	default:
		return nil, plugin.ErrUnsupported
	}
}

func decode(payload json.RawMessage, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func pathUnder(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
