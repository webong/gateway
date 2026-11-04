package integration

import (
	"net/netip"
	"testing"
)

func TestPublicEgressAddress(t *testing.T) {
	for _, test := range []struct {
		address string
		want    bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},

		// Neighbours of the special-use ranges stay reachable.
		{"100.63.255.255", true},
		{"100.128.0.0", true},
		{"198.17.255.255", true},
		{"198.20.0.0", true},

		{"10.0.0.1", false},
		{"239.255.255.255", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"127.0.0.1", false},
		{"169.254.169.254", false},
		{"0.0.0.0", false},
		{"0.1.2.3", false},
		{"255.255.255.255", false},
		{"::1", false},
		{"::", false},
		{"fd00::1", false},
		{"fe80::1", false},
		{"ff02::1", false},

		// Special-use space that netip does not classify as private.
		{"100.64.0.1", false},
		{"100.100.100.200", false},
		{"192.0.0.1", false},
		{"192.0.2.5", false},
		{"198.18.0.1", false},
		{"198.51.100.7", false},
		{"203.0.113.9", false},
		{"240.0.0.1", false},
		{"::10.0.0.1", false},
		{"100::1", false},
		{"2001::1", false},
		{"2001:db8::1", false},
		{"3fff::1", false},
		{"64:ff9b:1::1", false},

		// IPv4 carried in IPv6 is judged by the IPv4 address.
		{"::ffff:8.8.8.8", true},
		{"::ffff:10.0.0.1", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:169.254.169.254", false},
		{"::ffff:100.100.100.200", false},
		{"64:ff9b::808:808", true},
		{"64:ff9b::a00:1", false},
		{"64:ff9b::a9fe:a9fe", false},
		{"64:ff9b::6464:64c8", false},
		{"2002:808:808::1", true},
		{"2002:a00:1::1", false},
		{"2002:a9fe:a9fe::1", false},

		{"fe80::1%eth0", false},
	} {
		if got := PublicEgressAddress(netip.MustParseAddr(test.address)); got != test.want {
			t.Errorf("PublicEgressAddress(%s) = %v, want %v", test.address, got, test.want)
		}
	}

	if PublicEgressAddress(netip.Addr{}) {
		t.Error("the zero address must not be a public egress address")
	}
}

func TestEgressAddressAllowedWithPrivateNetworks(t *testing.T) {
	for _, test := range []struct {
		address string
		want    bool
	}{
		{"8.8.8.8", true},
		{"10.0.0.1", true},
		{"172.16.0.1", true},
		{"192.168.1.1", true},
		{"fd00::1", true},
		{"::ffff:10.0.0.1", true},
		{"64:ff9b::a00:1", true},
		{"2002:a00:1::1", true},

		// Private approval never reaches local or special-use space.
		{"127.0.0.1", false},
		{"::1", false},
		{"169.254.169.254", false},
		{"fe80::1", false},
		{"0.0.0.0", false},
		{"224.0.0.1", false},
		{"100.100.100.200", false},
		{"198.18.0.1", false},
		{"192.0.2.5", false},
		{"2001:db8::1", false},
		{"::ffff:127.0.0.1", false},
		{"64:ff9b::a9fe:a9fe", false},
	} {
		if got := EgressAddressAllowed(netip.MustParseAddr(test.address), true); got != test.want {
			t.Errorf("EgressAddressAllowed(%s, true) = %v, want %v", test.address, got, test.want)
		}
	}
}
