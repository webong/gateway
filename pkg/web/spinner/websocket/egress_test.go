package websocket

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

func TestDeliveryAdapterBlocksSpecialUseAddresses(t *testing.T) {
	adapter, err := NewDeliveryAdapter([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"127.0.0.1", "169.254.169.254", "100.100.100.200", "198.18.0.1", "::ffff:100.100.100.200"} {
		if adapter.addressAllowed(netip.MustParseAddr(address)) {
			t.Errorf("expected %s to be blocked", address)
		}
		_, err := adapter.safeDialContext(0)(context.Background(), "tcp", "["+address+"]:80")
		if err == nil || !strings.Contains(err.Error(), "blocked addresses") {
			t.Errorf("expected dial to %s to be refused, got %v", address, err)
		}
	}
	if !adapter.addressAllowed(netip.MustParseAddr("8.8.8.8")) {
		t.Error("expected a public address to be allowed")
	}
	if !adapter.addressAllowed(netip.MustParseAddr("10.1.2.3")) {
		t.Error("expected an address inside the private ceiling to be allowed")
	}
	if adapter.addressAllowed(netip.MustParseAddr("192.168.1.1")) {
		t.Error("expected an address outside the private ceiling to be blocked")
	}
}
