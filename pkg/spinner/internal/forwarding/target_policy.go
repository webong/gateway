//go:build !gateway_smoke

package forwarding

import (
	"net"
	"net/netip"

	"github.com/webong/gateway/arranger/integration"
)

// isAllowedTargetAddress is the production SSRF target policy. Only public
// addresses are dialed; private, loopback, link-local, and special-use
// addresses are never dialed.
func isAllowedTargetAddress(address net.IP) bool {
	parsed, ok := netip.AddrFromSlice(address)
	return ok && integration.PublicEgressAddress(parsed)
}
