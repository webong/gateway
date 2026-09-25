// Package gateway contains the protocol boundary between the Go transport
// plane and the PHP control plane. It does not know how PHP stores endpoints,
// subscribers, or migrations.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/webong/gateway/internal/automation"
)

const ProtocolVersion = "v1"

type PlanAction string

const (
	ActionRelay       PlanAction = "relay"
	ActionRespond     PlanAction = "respond"
	ActionPassThrough PlanAction = "pass_through"
	ActionAutomation  PlanAction = "automation"
)

var (
	ErrInvalidPlan       = errors.New("invalid route plan")
	ErrInvalidDelivery   = errors.New("invalid delivery")
	ErrMissingPlanResult = errors.New("route planner returned no plan")
)

// IngressRequest is the normalized request captured by Go at the public edge.
// The body is base64 encoded by encoding/json so arbitrary bytes can cross the
// JSON bridge safely. PHP decides what the path means.
type IngressRequest struct {
	DeliveryID string              `json:"delivery_id"`
	Protocol   Protocol            `json:"protocol,omitempty"`
	Event      EventKind           `json:"event,omitempty"`
	SessionID  string              `json:"session_id,omitempty"`
	Method     string              `json:"method"`
	Scheme     string              `json:"scheme,omitempty"`
	Host       string              `json:"host,omitempty"`
	Path       string              `json:"path"`
	RawQuery   string              `json:"raw_query,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       []byte              `json:"body,omitempty"`
}

// RoutePlan is returned by PHP after it validates the request, resolves the
// endpoint/subscriber registry, and binds the request to a route.
//
// ImmediateResponse is useful for provider verification or a PHP-generated
// response. Reply is one synchronous network delivery whose response is
// returned to the caller. Relays are asynchronous deliveries.
type RoutePlan struct {
	Version           string            `json:"version"`
	Action            PlanAction        `json:"action"`
	ImmediateResponse *Response         `json:"immediate_response,omitempty"`
	Reply             *Delivery         `json:"reply,omitempty"`
	Relays            []Delivery        `json:"relays,omitempty"`
	Automation        *Automation       `json:"automation,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// Automation source is selected by the planner and executed by the Go router.
// The router owns interpreter limits and the script capability surface.
type Automation struct {
	Language automation.Language `json:"language"`
	Source   string              `json:"source"`
}

// Delivery describes a destination resolved by PHP. Go still applies its
// outbound HTTP and DNS policy when it performs the delivery.
type Delivery struct {
	ID           string              `json:"id,omitempty"`
	SubscriberID string              `json:"subscriber_id,omitempty"`
	URL          string              `json:"url"`
	Method       string              `json:"method,omitempty"`
	RawQuery     string              `json:"raw_query,omitempty"`
	Headers      map[string][]string `json:"headers,omitempty"`
	Body         []byte              `json:"body,omitempty"`
}

type Response struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       []byte              `json:"body,omitempty"`
}

// Planner is implemented by the PHP/RoadRunner boundary. Keeping it small
// makes the transport and the Laravel integration independently testable.
type Planner interface {
	Plan(context.Context, IngressRequest) (RoutePlan, error)
}

// Executor is implemented by the Go transport plane. Deliver performs a
// synchronous request whose response is returned upstream. Enqueue submits an
// asynchronous relay without tying it to the caller's request lifetime.
type Executor interface {
	Deliver(context.Context, Delivery) (Response, error)
	Enqueue(Delivery) bool
}

// ProtocolPlanner is the protocol-neutral control-plane seam used by
// session-oriented adapters such as SMTP and WebSocket.
type ProtocolPlanner interface {
	PlanEvent(context.Context, GatewayEvent) (GatewayDecision, error)
}

// GatewayExecutor executes protocol-neutral deliveries resolved by PHP. The
// current transport implementation accepts HTTP destinations; other network
// protocols can add their own executor without changing the event contract.
type GatewayExecutor interface {
	EnqueueGateway(GatewayDelivery) bool
}

// GatewayDeliveryAdapter performs one resolved delivery. Adapters own their
// protocol lifecycle but do not own route selection, retry scheduling, or
// queue persistence.
type GatewayDeliveryAdapter interface {
	DeliverGateway(context.Context, GatewayDelivery) error
}

type GatewayDeliveryAdapterFunc func(context.Context, GatewayDelivery) error

func (f GatewayDeliveryAdapterFunc) DeliverGateway(ctx context.Context, delivery GatewayDelivery) error {
	return f(ctx, delivery)
}

// GatewayDeliveryQueue durably accepts a resolved delivery before ingress is
// acknowledged. Implementations own recovery and retry scheduling.
type GatewayDeliveryQueue interface {
	EnqueueGateway(GatewayDelivery) error
}

func (p RoutePlan) Validate() error {
	if p.Version != ProtocolVersion {
		return fmt.Errorf("%w: unsupported version %q", ErrInvalidPlan, p.Version)
	}

	switch p.Action {
	case ActionPassThrough:
		if p.ImmediateResponse != nil || p.Reply != nil || len(p.Relays) > 0 || p.Automation != nil {
			return fmt.Errorf("%w: pass-through plan cannot contain response or deliveries", ErrInvalidPlan)
		}
	case ActionRespond:
		if p.ImmediateResponse == nil || p.Reply != nil || len(p.Relays) > 0 || p.Automation != nil {
			return fmt.Errorf("%w: respond plan requires only an immediate response", ErrInvalidPlan)
		}
		if err := p.ImmediateResponse.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidPlan, err)
		}
	case ActionRelay:
		if p.ImmediateResponse != nil || p.Automation != nil {
			return fmt.Errorf("%w: relay plan cannot contain an immediate response", ErrInvalidPlan)
		}
		if p.Reply == nil && len(p.Relays) == 0 {
			return fmt.Errorf("%w: relay plan requires a reply or relay delivery", ErrInvalidPlan)
		}
		if p.Reply != nil {
			if err := p.Reply.Validate(); err != nil {
				return fmt.Errorf("%w: reply: %v", ErrInvalidPlan, err)
			}
		}
		for index := range p.Relays {
			if err := p.Relays[index].Validate(); err != nil {
				return fmt.Errorf("%w: relay %d: %v", ErrInvalidPlan, index, err)
			}
		}
	case ActionAutomation:
		if p.Automation == nil || p.ImmediateResponse != nil || p.Reply != nil {
			return fmt.Errorf("%w: automation plan requires automation and optional relays", ErrInvalidPlan)
		}
		if strings.TrimSpace(p.Automation.Source) == "" {
			return fmt.Errorf("%w: automation source is required", ErrInvalidPlan)
		}
		switch p.Automation.Language {
		case automation.LanguageWebhookScript, automation.LanguageLua, automation.LanguageJavaScript:
		default:
			return fmt.Errorf("%w: unsupported automation language %q", ErrInvalidPlan, p.Automation.Language)
		}
		for index := range p.Relays {
			if err := p.Relays[index].Validate(); err != nil {
				return fmt.Errorf("%w: relay %d: %v", ErrInvalidPlan, index, err)
			}
		}
	default:
		return fmt.Errorf("%w: unsupported action %q", ErrInvalidPlan, p.Action)
	}

	return nil
}

func (d Delivery) Validate() error {
	if strings.TrimSpace(d.URL) == "" {
		return fmt.Errorf("%w: URL is required", ErrInvalidDelivery)
	}
	if d.Method != "" {
		if _, ok := allowedMethods[strings.ToUpper(d.Method)]; !ok {
			return fmt.Errorf("%w: unsupported method %q", ErrInvalidDelivery, d.Method)
		}
	}
	return nil
}

func (r Response) Validate() error {
	if r.StatusCode < 100 || r.StatusCode > 599 {
		return fmt.Errorf("status code %d is outside the HTTP range", r.StatusCode)
	}
	return nil
}

var allowedMethods = map[string]struct{}{
	http.MethodConnect: {},
	http.MethodDelete:  {},
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodOptions: {},
	http.MethodPatch:   {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodTrace:   {},
}
