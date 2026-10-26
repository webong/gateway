package forwarding

import (
	"context"
	"time"
)

type HTTPTransport struct {
	HTTPProxy      string
	HTTPSProxy     string
	NoProxy        []string
	ConnectTimeout time.Duration
	RequestTimeout time.Duration
	// PrivateCallbackCIDRs is a separate mail callback grant, bounded by a separate deployment ceiling.
	PrivateCallbackCIDRs string
}

// ForwardRequest is the transport-level request sent to a subscriber.
type ForwardRequest struct {
	TargetURL   string
	RequestBody []byte
	Method      string
	RawQuery    string
	Headers     map[string][]string
	Context     context.Context
	Transport   HTTPTransport
}
