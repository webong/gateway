package forwarding

import "context"

// ForwardRequest is the transport-level request sent to a subscriber.
type ForwardRequest struct {
	TargetURL   string
	RequestBody []byte
	Method      string
	RawQuery    string
	Headers     map[string][]string
	Context     context.Context
}
