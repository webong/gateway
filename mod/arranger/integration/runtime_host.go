package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/automation"
)

// RuntimeHost exposes only the capabilities declared by an Extension's descriptor.
// It contains no address or credential for Planner.
type RuntimeHost struct {
	caller    plugin.ReverseCaller
	languages []automation.Language
}

type RuntimeExecutor interface {
	Executor
	GatewayExecutor
	IdempotentGatewayExecutor
	GatewayDeliveryAdapterSupport
}

func NewRuntimeHost(caller plugin.ReverseCaller, languages []automation.Language) *RuntimeHost {
	return &RuntimeHost{caller: caller, languages: append([]automation.Language(nil), languages...)}
}

func (h *RuntimeHost) Planner() Planner                 { return plannerClient{caller: h.caller} }
func (h *RuntimeHost) ProtocolPlanner() ProtocolPlanner { return plannerClient{caller: h.caller} }
func (h *RuntimeHost) Executor() RuntimeExecutor        { return deliveryClient{caller: h.caller} }
func (h *RuntimeHost) PassThrough() http.Handler        { return passThroughClient{caller: h.caller} }
func (h *RuntimeHost) PrivatePlanner() http.Handler {
	return passThroughClient{caller: h.caller, operation: OperationPrivatePassThrough}
}
func (h *RuntimeHost) Automation() automation.Executor {
	return automationClient{caller: h.caller, languages: append([]automation.Language(nil), h.languages...)}
}
func (h *RuntimeHost) Schedules() automation.ScheduleRegistrar {
	return scheduleClient{caller: h.caller}
}
func (h *RuntimeHost) Observations() ObservationSink { return observationClient{caller: h.caller} }

type plannerClient struct{ caller plugin.ReverseCaller }

func (p plannerClient) Plan(ctx context.Context, input IngressRequest) (RoutePlan, error) {
	var result RoutePlan
	err := call(ctx, p.caller, PlannerContract, OperationPlanHTTP, input, &result)
	return result, err
}

func (p plannerClient) PlanEvent(ctx context.Context, input GatewayEvent) (GatewayDecision, error) {
	var result GatewayDecision
	err := call(ctx, p.caller, PlannerContract, OperationPlanEvent, input, &result)
	return result, err
}

type deliveryClient struct{ caller plugin.ReverseCaller }

func (d deliveryClient) Deliver(ctx context.Context, input Delivery) (Response, error) {
	var result DeliveryResponse
	err := call(ctx, d.caller, DeliveryContract, OperationDeliver, input, &result)
	return result.Response, err
}

func (d deliveryClient) Enqueue(input Delivery) bool {
	var result EnqueueResponse
	return call(context.Background(), d.caller, DeliveryContract, OperationEnqueue, input, &result) == nil && result.Accepted
}

func (d deliveryClient) EnqueueGateway(input GatewayDelivery) bool {
	var result EnqueueResponse
	return call(context.Background(), d.caller, DeliveryContract, OperationEnqueueGateway, input, &result) == nil && result.Accepted
}

func (d deliveryClient) EnqueueGatewayIdempotent(input GatewayDelivery, id, fingerprint string, expiresAt time.Time) (bool, error) {
	var result IdempotentEnqueueResponse
	err := call(context.Background(), d.caller, DeliveryContract, OperationEnqueueGatewayIdempotent,
		IdempotentEnqueueRequest{Delivery: input, ID: id, Fingerprint: fingerprint, ExpiresAt: expiresAt}, &result)
	if err != nil {
		return false, err
	}
	if result.Conflict {
		return false, ErrIdempotencyConflict
	}
	return result.Replayed, nil
}

func (d deliveryClient) SupportsAdapter(adapter string) bool {
	var result DeliveryAdapterSupportResponse
	return call(context.Background(), d.caller, DeliveryContract, OperationSupportsDeliveryAdapter,
		DeliveryAdapterSupportRequest{Adapter: adapter}, &result) == nil && result.Supported
}

func (d deliveryClient) DurableGatewayDeliveries() bool {
	var result DurableResponse
	return call(context.Background(), d.caller, DeliveryContract, OperationDurable, struct{}{}, &result) == nil && result.Enabled
}

type automationClient struct {
	caller    plugin.ReverseCaller
	languages []automation.Language
}

func (a automationClient) Supports(language automation.Language) bool {
	for _, candidate := range a.languages {
		if candidate == language {
			return true
		}
	}
	return false
}

func (a automationClient) Languages() []automation.Language {
	return append([]automation.Language(nil), a.languages...)
}

func (a automationClient) Execute(ctx context.Context, script automation.Script, input automation.Input) (automation.Outcome, error) {
	if !a.Supports(script.Language) {
		return automation.Outcome{}, fmt.Errorf("%w %q", automation.ErrUnsupportedLanguage, script.Language)
	}
	var result AutomationResponse
	err := call(ctx, a.caller, AutomationContract, OperationExecuteAutomation, AutomationRequest{Script: script, Input: input}, &result)
	return result.Outcome, err
}

type scheduleClient struct{ caller plugin.ReverseCaller }

func (s scheduleClient) RegisterSchedules(ctx context.Context, origin string, schedules []automation.Schedule) error {
	return call(ctx, s.caller, ScheduleContract, OperationRegisterSchedules, ScheduleRequest{Origin: origin, Schedules: schedules}, nil)
}

type observationClient struct{ caller plugin.ReverseCaller }

func (o observationClient) RecordObservation(ctx context.Context, observation Observation) error {
	if err := observation.Validate(); err != nil {
		return err
	}
	return call(ctx, o.caller, ObservationContract, OperationRecordObservation, observation, nil)
}

type passThroughClient struct {
	caller    plugin.ReverseCaller
	operation string
}

func (p passThroughClient) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(writer, "Planner request body is unavailable", http.StatusBadRequest)
		return
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	var result PassThroughResponse
	operation := p.operation
	if operation == "" {
		operation = OperationPassThrough
	}
	err = call(request.Context(), p.caller, PlannerContract, operation, PassThroughRequest{
		Method: request.Method, Scheme: scheme, Host: request.Host, Path: request.URL.Path,
		RawQuery: request.URL.RawQuery, Headers: request.Header.Clone(), Body: body, RemoteAddr: request.RemoteAddr,
	}, &result)
	if err != nil {
		http.Error(writer, "Planner pass-through is unavailable", http.StatusBadGateway)
		return
	}
	for name, values := range result.Headers {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	writer.WriteHeader(result.Status)
	_, _ = writer.Write(result.Body)
}

func call(ctx context.Context, caller plugin.ReverseCaller, ref plugin.ContractRef, operation string, input any, output any) error {
	if caller == nil {
		return errorsUnavailable(ref, operation)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	response, err := caller.Call(ctx, ref, operation, payload)
	if err != nil {
		return err
	}
	if output == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(response))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("runtime host returned trailing data")
	}
	return nil
}

func errorsUnavailable(ref plugin.ContractRef, operation string) error {
	return fmt.Errorf("runtime host capability %s/%s %s is unavailable", strings.TrimSpace(ref.Name), strings.TrimSpace(ref.Version), operation)
}
