package forwarding

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/webong/gateway/arranger/integration"
	"github.com/webong/proxy/httpclient"
	"github.com/webong/proxy/httpprofile"
)

// ErrInvalidTransport reports per-delivery connection settings that do not
// satisfy Proxy's portable HTTP profile contract. Retrying cannot fix them.
var ErrInvalidTransport = errors.New("invalid outbound HTTP transport")

// TransportFromWire adapts the Planner's endpoint-owned transport settings to
// the Forwarder's request. Callers keep the private-callback grant, which is a
// separate Gateway authorization and is not part of a portable profile.
func TransportFromWire(wire *integration.HTTPTransport) HTTPTransport {
	if wire == nil {
		return HTTPTransport{}
	}
	transport := HTTPTransport{
		ConnectTimeout: wireDuration(wire.ConnectTimeoutSeconds),
		RequestTimeout: wireDuration(wire.RequestTimeoutSeconds),
	}
	if wire.Proxy != nil {
		transport.HTTPProxy = wire.Proxy.HTTP
		transport.HTTPSProxy = wire.Proxy.HTTPS
		transport.NoProxy = append([]string(nil), wire.Proxy.NoProxy...)
	}
	return transport
}

func wireDuration(seconds float64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

// usesDefaults reports whether the delivery needs none of the profile-owned
// connection settings, so the Forwarder's shared client serves it.
func (t HTTPTransport) usesDefaults() bool {
	return t.HTTPProxy == "" && t.HTTPSProxy == "" && len(t.NoProxy) == 0 &&
		t.ConnectTimeout == 0 && t.RequestTimeout == 0
}

// profile expresses the delivery's connection settings as a portable Proxy
// HTTP profile, the same contract the Planner's Proxy library exports.
func (t HTTPTransport) profile() httpprofile.Profile {
	profile := httpprofile.Profile{
		Version:               1,
		ConnectTimeoutSeconds: t.ConnectTimeout.Seconds(),
		RequestTimeoutSeconds: t.RequestTimeout.Seconds(),
	}
	if t.HTTPProxy != "" || t.HTTPSProxy != "" || len(t.NoProxy) > 0 {
		profile.Proxy = &httpprofile.Proxy{
			HTTP:    t.HTTPProxy,
			HTTPS:   t.HTTPSProxy,
			NoProxy: append([]string(nil), t.NoProxy...),
		}
	}
	return profile
}

// profileClient builds the HTTP client for one connection profile.
//
// Proxy owns proxy selection, no-proxy matching, SOCKS routing and the connect
// and request timeouts. Gateway supplies the base transport, so every connection
// the client makes, including the hop to a proxy, goes through Gateway's
// address policy and keeps its connection limits. The returned client does not
// follow environment proxy variables.
func (f *Forwarder) profileClient(profile httpprofile.Profile) (*http.Client, error) {
	client, err := httpclient.New(profile, f.transport)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransport, err)
	}
	if profile.RequestTimeoutSeconds == 0 {
		// An omitted request timeout keeps Gateway's deployment-wide budget.
		client.Timeout = f.client.Timeout
	}
	if profile.Proxy == nil {
		return client, nil
	}

	// Gateway never sends a loopback target to a proxy: it is dialed directly,
	// where the address policy decides. Proxy's profile has no such implicit rule.
	direct := profile
	direct.Proxy = nil
	directClient, err := httpclient.New(direct, f.transport)
	if err != nil {
		client.CloseIdleConnections()
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransport, err)
	}
	client.Transport = &loopbackDirect{proxied: client.Transport, direct: directClient.Transport}
	return client, nil
}

// loopbackDirect sends loopback and localhost targets around the profile's
// proxy. Everything else follows the profile.
type loopbackDirect struct {
	proxied http.RoundTripper
	direct  http.RoundTripper
}

func (t *loopbackDirect) RoundTrip(request *http.Request) (*http.Response, error) {
	if loopbackTarget(request.URL.Hostname()) {
		return t.direct.RoundTrip(request)
	}
	return t.proxied.RoundTrip(request)
}

func (t *loopbackDirect) CloseIdleConnections() {
	for _, transport := range []http.RoundTripper{t.proxied, t.direct} {
		if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
}

func loopbackTarget(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
