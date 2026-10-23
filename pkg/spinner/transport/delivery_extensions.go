package transport

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/internal/config"
)

// DeliveryAdapterFactory builds a trusted adapter for each Spinner queue.
// A linked provider module registers its factories before runtime startup.
// Returning nil, nil leaves this worker unable to claim that adapter's work.
type DeliveryAdapterFactory func(*config.Config) (bridge.GatewayDeliveryAdapter, error)

var deliveryAdapterFactories = struct {
	sync.RWMutex
	values map[string]DeliveryAdapterFactory
}{values: make(map[string]DeliveryAdapterFactory)}

// RegisterDeliveryAdapterFactory is the bundled Spinner and Caddy extension
// point. Names are namespaces, not tenant-controlled input or plugin paths.
func RegisterDeliveryAdapterFactory(name string, factory DeliveryAdapterFactory) error {
	if !validProviderAdapterName(name) || factory == nil {
		return errors.New("provider delivery adapter requires a namespaced name and factory")
	}
	deliveryAdapterFactories.Lock()
	defer deliveryAdapterFactories.Unlock()
	if _, exists := deliveryAdapterFactories.values[name]; exists {
		return fmt.Errorf("delivery adapter %q is already registered", name)
	}
	deliveryAdapterFactories.values[name] = factory
	return nil
}

func registeredDeliveryAdapters(settings *config.Config) (map[string]bridge.GatewayDeliveryAdapter, error) {
	deliveryAdapterFactories.RLock()
	factories := make(map[string]DeliveryAdapterFactory, len(deliveryAdapterFactories.values))
	for name, factory := range deliveryAdapterFactories.values {
		factories[name] = factory
	}
	deliveryAdapterFactories.RUnlock()
	adapters := make(map[string]bridge.GatewayDeliveryAdapter, len(factories))
	for name, factory := range factories {
		adapter, err := factory(settings)
		if err != nil {
			return nil, fmt.Errorf("construct delivery adapter %q: %w", name, err)
		}
		if adapter != nil {
			adapters[name] = adapter
		}
	}
	return adapters, nil
}

func validProviderAdapterName(name string) bool {
	if len(name) < 3 || len(name) > 64 || !strings.Contains(name, ".") || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, character := range name[1:] {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return !strings.HasPrefix(name, "gateway.") && name != "mailbox.smtp"
}
