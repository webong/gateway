// Package networkpolicy validates deployment-level network safety ceilings.
package networkpolicy

import (
	"fmt"
	"net/netip"
	"strings"
)

var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("fc00::/7"),
}

// ParsePrivatePrefixes accepts only canonical prefixes contained by RFC 1918
// or unique-local IPv6 space. Callers retain ownership of the setting name and
// of the policy that consumes the resulting ceiling.
func ParsePrivatePrefixes(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var result []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil || prefix.Addr().Is4In6() || prefix != prefix.Masked() {
			return nil, fmt.Errorf("invalid private network prefix %q", item)
		}
		private := false
		for _, allowed := range privateRanges {
			if prefix.Bits() >= allowed.Bits() && allowed.Contains(prefix.Addr()) {
				private = true
				break
			}
		}
		if !private {
			return nil, fmt.Errorf("network prefix %q is outside private address space", item)
		}
		result = append(result, prefix)
	}
	return result, nil
}
