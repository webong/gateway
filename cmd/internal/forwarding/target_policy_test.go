//go:build !webrelay_smoke

package forwarding

import (
	"net"
	"testing"
)

func TestProductionTargetPolicyBlocksPrivateAndLoopbackAddresses(t *testing.T) {
	for _, address := range []string{
		"127.0.0.1",
		"::1",
		"10.0.0.1",
		"192.168.1.1",
		"169.254.1.1",
	} {
		if isAllowedTargetAddress(net.ParseIP(address)) {
			t.Errorf("expected target address %s to be blocked", address)
		}
	}

	if !isAllowedTargetAddress(net.ParseIP("8.8.8.8")) {
		t.Error("expected a public global-unicast address to be allowed")
	}
}
