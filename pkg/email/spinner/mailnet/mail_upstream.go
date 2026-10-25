package mailnet

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

var blockedMailNetworks = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// MailTLSDialer is the internal seam used by mailbox transport tests.
type MailTLSDialer func(context.Context, string, string, string, time.Duration, *x509.CertPool) (net.Conn, error)

// ValidMailUpstream checks a Planner-selected TLS endpoint. Network policy is
// enforced against resolved IPs at dial time, not just against a DNS name.
func ValidMailUpstream(address, serverName, networkScope string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" || host != strings.ToLower(host) ||
		serverName == "" || serverName != strings.ToLower(serverName) ||
		strings.ContainsAny(serverName, ":/ \t\r\n") ||
		(networkScope != "public" && networkScope != "private") {
		return false
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return false
	}
	for _, name := range []string{host, serverName} {
		if len(name) > 253 || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") ||
			strings.ContainsAny(name, " /\t\r\n\\@") {
			return false
		}
	}
	return true
}

// DialMailUpstream resolves once, checks every candidate IP, and dials the
// checked IP rather than asking net.Dialer to resolve the hostname again.
func DialMailUpstream(ctx context.Context, address, networkScope string, timeout time.Duration) (net.Conn, error) {
	return dialMailUpstream(ctx, address, networkScope, timeout, net.DefaultResolver.LookupIPAddr,
		(&net.Dialer{Timeout: timeout}).DialContext)
}

// DialTLSMailUpstream preserves certificate verification against the Planner
// server name while the TCP connection stays pinned to an approved IP.
func DialTLSMailUpstream(ctx context.Context, address, serverName, networkScope string,
	timeout time.Duration, roots *x509.CertPool) (net.Conn, error) {
	connection, err := DialMailUpstream(ctx, address, networkScope, timeout)
	if err != nil {
		return nil, err
	}
	secured := tls.Client(connection, &tls.Config{ServerName: serverName, RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err := secured.HandshakeContext(ctx); err != nil {
		_ = secured.Close()
		return nil, err
	}
	return secured, nil
}

func dialMailUpstream(ctx context.Context, address, networkScope string, timeout time.Duration,
	lookup func(context.Context, string) ([]net.IPAddr, error),
	dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	if timeout <= 0 || (networkScope != "public" && networkScope != "private") {
		return nil, errors.New("invalid mail network policy")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return nil, errors.New("invalid mail upstream address")
	}
	addresses, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var dialErr error
	for _, candidate := range addresses {
		if !mailAddressAllowed(candidate.IP, networkScope) {
			continue
		}
		connection, err := dial(ctx, "tcp", net.JoinHostPort(candidate.IP.String(), port))
		if err == nil {
			return connection, nil
		}
		dialErr = err
	}
	if dialErr != nil {
		return nil, dialErr
	}
	return nil, errors.New("mail upstream resolves only to blocked addresses")
}

func mailAddressAllowed(ip net.IP, scope string) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsLoopback() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsUnspecified() {
		return false
	}
	for _, blocked := range blockedMailNetworks {
		if blocked.Contains(address) {
			return false
		}
	}
	return !address.IsPrivate() || scope == "private"
}
