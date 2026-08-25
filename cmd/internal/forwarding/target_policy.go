//go:build !netgateway_smoke

package forwarding

import "net"

// isAllowedTargetAddress is the production SSRF target policy. Private,
// loopback, link-local, and unspecified addresses are never dialed.
func isAllowedTargetAddress(address net.IP) bool {
	return address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLoopback() && !address.IsLinkLocalUnicast() && !address.IsUnspecified()
}
