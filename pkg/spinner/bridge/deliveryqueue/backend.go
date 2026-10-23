package deliveryqueue

import (
	"context"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

// Backend is the runtime contract shared by local and distributed durable
// delivery queues. It includes the private admin and Email submission seams.
type Backend interface {
	EnqueueGateway(bridge.GatewayDelivery) error
	EnqueueGatewayWithID(bridge.GatewayDelivery) (string, error)
	EnqueueGatewayIdempotent(bridge.GatewayDelivery, string, string, time.Time) (bool, error)
	Counts() (int, int, error)
	Status(string) (Status, error)
	List(State, int) ([]Status, error)
	ListEvent(string, int) ([]Status, error)
	Retry(string) error
	Close(context.Context) error
}

var _ Backend = (*Queue)(nil)
