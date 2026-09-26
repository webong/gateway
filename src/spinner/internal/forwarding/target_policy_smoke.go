//go:build gateway_smoke

package forwarding

import "net"

// This policy exists only in the explicitly tagged local integration binary.
// It permits the smoke test's loopback receiver; production builds use
// target_policy.go and retain the private-address SSRF protection.
func isAllowedTargetAddress(address net.IP) bool {
	return (address.IsGlobalUnicast() || address.IsLoopback()) && !address.IsUnspecified()
}
