package main

import (
	"net/netip"
	"testing"
)

func TestMailboxCallbackBlocksSpecialUseAddresses(t *testing.T) {
	callback := &mailboxCallback{private: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	for _, address := range []string{"127.0.0.1", "169.254.169.254", "100.100.100.200", "198.18.0.1", "192.168.1.1"} {
		if callback.allowed(netip.MustParseAddr(address)) {
			t.Errorf("expected %s to be blocked", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "10.1.2.3"} {
		if !callback.allowed(netip.MustParseAddr(address)) {
			t.Errorf("expected %s to be allowed", address)
		}
	}
}
