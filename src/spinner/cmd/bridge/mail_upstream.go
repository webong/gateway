package gateway

import (
	"net"
	"strconv"
	"strings"
)

// ApprovedMailUpstream constrains a Planner-selected endpoint to an exact
// deployment-approved TLS hostname. A DNS name may only be approved by the
// deployer; Planner cannot expand this ceiling.
func ApprovedMailUpstream(address, serverName string, allowedHosts []string) bool {
	host, port, err := net.SplitHostPort(address)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || host == "" || port == "" || host != strings.ToLower(host) ||
		serverName == "" || strings.ContainsAny(serverName, ":/ \t\r\n") || len(allowedHosts) == 0 || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return false
	}
	for _, allowed := range allowedHosts {
		if allowed == host {
			return true
		}
	}
	return false
}
