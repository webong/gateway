package spinner

import protocol "github.com/webong/gateway/arranger/protocol"

const (
	ProtocolVersion = protocol.ProtocolVersion

	ActionRelay       = protocol.ActionRelay
	ActionRespond     = protocol.ActionRespond
	ActionPassThrough = protocol.ActionPassThrough
	ActionAutomation  = protocol.ActionAutomation
	ProtocolHTTP      = protocol.ProtocolHTTP
	EventRequest      = protocol.EventRequest
)

type (
	Planner             = protocol.Planner
	Executor            = protocol.Executor
	IngressRequest      = protocol.IngressRequest
	RuntimeCapabilities = protocol.RuntimeCapabilities
	RoutePlan           = protocol.RoutePlan
	Automation          = protocol.Automation
	Delivery            = protocol.Delivery
	Response            = protocol.Response
)
