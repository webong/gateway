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
)

const ProtocolVersion = "v1"

type PlanAction string

const (
	ActionRelay       PlanAction = "relay"
	ActionRespond     PlanAction = "respond"
	ActionPassThrough PlanAction = "pass_through"
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
	Metadata          map[string]string `json:"metadata,omitempty"`
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

func (p RoutePlan) Validate() error {
	if p.Version != ProtocolVersion {
		return fmt.Errorf("%w: unsupported version %q", ErrInvalidPlan, p.Version)
	}

	switch p.Action {
	case ActionPassThrough:
		if p.ImmediateResponse != nil || p.Reply != nil || len(p.Relays) > 0 {
			return fmt.Errorf("%w: pass-through plan cannot contain response or deliveries", ErrInvalidPlan)
		}
	case ActionRespond:
		if p.ImmediateResponse == nil || p.Reply != nil || len(p.Relays) > 0 {
			return fmt.Errorf("%w: respond plan requires only an immediate response", ErrInvalidPlan)
		}
		if err := p.ImmediateResponse.Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidPlan, err)
		}
	case ActionRelay:
		if p.ImmediateResponse != nil {
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
