package integration

import "net/netip"

// nonPublicEgressNetworks are special-use ranges that Go's netip classifiers do
// not report as private, loopback, or link-local but that are never a public
// destination: shared address space (which includes some clouds' metadata
// endpoints), benchmarking and documentation blocks, and reserved space.
var nonPublicEgressNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.2/32"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/8"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("100:0:0:1::/64"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

var (
	nat64Prefix             = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour               = netip.MustParsePrefix("2002::/16")
	ietfProtocolAssignments = netip.MustParsePrefix("2001::/23")
	publicIETFAssignments   = []netip.Prefix{
		netip.MustParsePrefix("2001:1::1/128"),
		netip.MustParsePrefix("2001:1::2/128"),
		netip.MustParsePrefix("2001:1::3/128"),
		netip.MustParsePrefix("2001:3::/32"),
		netip.MustParsePrefix("2001:4:112::/48"),
		netip.MustParsePrefix("2001:20::/28"),
		netip.MustParsePrefix("2001:30::/28"),
	}
)

// PublicEgressAddress reports whether an outbound connection may target
// address without an operator-approved private network.
//
// It accepts only globally routable unicast addresses. Private, loopback,
// link-local, multicast, unspecified, and special-use addresses are refused,
// including when an IPv4 address is carried in an IPv4-mapped, NAT64, or 6to4
// IPv6 address. NAT64 and 6to4 addresses are judged by the IPv4 address they
// embed so DNS64 deployments can still reach public IPv4-only hosts.
//
// Callers must apply it to the address they dial, after resolution.
func PublicEgressAddress(address netip.Addr) bool {
	return EgressAddressAllowed(address, false)
}

// EgressAddressAllowed is PublicEgressAddress for a destination whose operator
// may have approved private networks. When allowPrivate is set, RFC 1918 and
// unique-local addresses are accepted as well; loopback, link-local,
// multicast, unspecified, and special-use addresses are still refused.
func EgressAddressAllowed(address netip.Addr, allowPrivate bool) bool {
	address = address.Unmap()
	if !address.IsValid() {
		return false
	}
	// Prefix.Contains never matches a zoned address.
	address = address.WithZone("")
	if address.Is6() {
		bytes := address.As16()
		switch {
		case nat64Prefix.Contains(address):
			return EgressAddressAllowed(netip.AddrFrom4([4]byte(bytes[12:16])), allowPrivate)
		case sixToFour.Contains(address):
			return EgressAddressAllowed(netip.AddrFrom4([4]byte(bytes[2:6])), allowPrivate)
		}
	}
	if !address.IsGlobalUnicast() || (address.IsPrivate() && !allowPrivate) || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsUnspecified() {
		return false
	}
	for _, network := range nonPublicEgressNetworks {
		if network.Contains(address) {
			return false
		}
	}
	if ietfProtocolAssignments.Contains(address) {
		for _, network := range publicIETFAssignments {
			if network.Contains(address) {
				return true
			}
		}
		return false
	}
	return true
}
