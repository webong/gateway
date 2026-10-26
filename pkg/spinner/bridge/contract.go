// Package gateway retains Spinner's compatibility import path for the
// transport-neutral protocol contract. The contract itself lives in Arranger
// so Gateway Extensions do not depend on Spinner's implementation module.
package gateway

import protocol "github.com/webong/gateway/arranger/integration"

const (
	ProtocolVersion = protocol.ProtocolVersion

	ActionRelay       = protocol.ActionRelay
	ActionRespond     = protocol.ActionRespond
	ActionPassThrough = protocol.ActionPassThrough
	ActionAutomation  = protocol.ActionAutomation

	ProtocolHTTP      = protocol.ProtocolHTTP
	ProtocolWebSocket = protocol.ProtocolWebSocket
	ProtocolWebstream = protocol.ProtocolWebstream
	ProtocolSMTP      = protocol.ProtocolSMTP
	ProtocolPOP3      = protocol.ProtocolPOP3
	ProtocolIMAP      = protocol.ProtocolIMAP
	ProtocolDNS       = protocol.ProtocolDNS

	EventRequest      = protocol.EventRequest
	EventConnect      = protocol.EventConnect
	EventMessage      = protocol.EventMessage
	EventClose        = protocol.EventClose
	EventTransaction  = protocol.EventTransaction
	EventRecipient    = protocol.EventRecipient
	EventAuthenticate = protocol.EventAuthenticate
	EventQuery        = protocol.EventQuery

	GatewayAccept     = protocol.GatewayAccept
	GatewayReject     = protocol.GatewayReject
	GatewayRespond    = protocol.GatewayRespond
	GatewayDeliver    = protocol.GatewayDeliver
	GatewaySubmit     = protocol.GatewaySubmit
	GatewayQuarantine = protocol.GatewayQuarantine
	GatewayClose      = protocol.GatewayClose
	GatewayStream     = protocol.GatewayStream
	GatewayAutomate   = protocol.GatewayAutomate
)

var (
	ErrInvalidPlan         = protocol.ErrInvalidPlan
	ErrInvalidDelivery     = protocol.ErrInvalidDelivery
	ErrPermanentDelivery   = protocol.ErrPermanentDelivery
	ErrUncertainDelivery   = protocol.ErrUncertainDelivery
	ErrMissingPlanResult   = protocol.ErrMissingPlanResult
	ErrIdempotencyConflict = protocol.ErrIdempotencyConflict
)

type (
	PlanAction                    = protocol.PlanAction
	IngressRequest                = protocol.IngressRequest
	RuntimeCapabilities           = protocol.RuntimeCapabilities
	RoutePlan                     = protocol.RoutePlan
	Automation                    = protocol.Automation
	Delivery                      = protocol.Delivery
	HTTPTransport                 = protocol.HTTPTransport
	HTTPProxy                     = protocol.HTTPProxy
	Response                      = protocol.Response
	Planner                       = protocol.Planner
	Executor                      = protocol.Executor
	ProtocolPlanner               = protocol.ProtocolPlanner
	GatewayExecutor               = protocol.GatewayExecutor
	DurableGatewayExecutor        = protocol.DurableGatewayExecutor
	IdempotentGatewayExecutor     = protocol.IdempotentGatewayExecutor
	GatewayDeliveryAdapterSupport = protocol.GatewayDeliveryAdapterSupport
	DurableGatewayBatchExecutor   = protocol.DurableGatewayBatchExecutor
	GatewayDeliveryAdapter        = protocol.GatewayDeliveryAdapter
	GatewayDeliveryResult         = protocol.GatewayDeliveryResult
	GatewayDeliveryResultAdapter  = protocol.GatewayDeliveryResultAdapter
	GatewayDeliveryAdapterFunc    = protocol.GatewayDeliveryAdapterFunc
	GatewayDeliveryQueue          = protocol.GatewayDeliveryQueue
	Protocol                      = protocol.Protocol
	EventKind                     = protocol.EventKind
	GatewayEvent                  = protocol.GatewayEvent
	GatewayAction                 = protocol.GatewayAction
	GatewayDelivery               = protocol.GatewayDelivery
	GatewayDecision               = protocol.GatewayDecision
	DeliveryFailureRequest        = protocol.DeliveryFailureRequest
	DeliveryFailureHandler        = protocol.DeliveryFailureHandler
)

func GatewayEventFromHTTP(ingress IngressRequest) GatewayEvent {
	return protocol.GatewayEventFromHTTP(ingress)
}

func validAdapterName(name string) bool {
	return protocol.ValidAdapterName(name)
}
