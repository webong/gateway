package forwarding

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webong/gateway/arranger/integration"
	"github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
)

// The PHP resolver test (tests/Feature/EndpointHttpTransportResolverTest.php)
// reads this same fixture: profiles are registered there, serialized by the
// Planner, and routed here from the wire form it asserts.
type transportFixture struct {
	Profiles []struct {
		Name             string                    `json:"name"`
		Wire             integration.HTTPTransport `json:"wire"`
		ConnectTimeoutMS *int64                    `json:"connect_timeout_ms"`
		RequestTimeoutMS *int64                    `json:"request_timeout_ms"`
		Requests         []struct {
			URL   string  `json:"url"`
			Proxy *string `json:"proxy"`
		} `json:"requests"`
	} `json:"profiles"`
	InvalidWire []struct {
		Name string                    `json:"name"`
		Wire integration.HTTPTransport `json:"wire"`
	} `json:"invalid_wire"`
}

func loadTransportFixture(t *testing.T) transportFixture {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "mod", "arranger", "integration", "testdata", "http_transport_profiles.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture transportFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(fixture.Profiles) == 0 || len(fixture.InvalidWire) == 0 {
		t.Fatal("transport fixture is empty")
	}
	return fixture
}

// dialLog replaces the base dialer Gateway hands to Proxy's client factory and
// records every address a connection is attempted to, without connecting. The
// first dial of a request is the proxy when the profile selects one and the
// target otherwise, so it shows proxy selection without a network.
type dialLog struct {
	mu        sync.Mutex
	addresses []string
}

func (l *dialLog) dial(_ context.Context, _, address string) (net.Conn, error) {
	l.mu.Lock()
	l.addresses = append(l.addresses, address)
	l.mu.Unlock()
	return nil, errors.New("dial recorded")
}

func (l *dialLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	taken := l.addresses
	l.addresses = nil
	return taken
}

func newRecordingForwarder(t *testing.T) (*Forwarder, *dialLog) {
	t.Helper()
	forwarder := NewForwarder(&config.Config{
		MaxBodySize: 1024, RequestTimeout: 3 * time.Second, IdleConnTimeout: time.Second,
		MaxIdleConns: 4, MaxConnsPerHost: 4,
	}, logging.NewLogger("error"))
	log := &dialLog{}
	forwarder.transport.DialContext = log.dial
	t.Cleanup(forwarder.CloseIdleConnections)
	return forwarder, log
}

func canonicalDial(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return strings.ToLower(address)
	}
	return strings.ToLower(net.JoinHostPort(strings.TrimSuffix(host, "."), port))
}

// expectedDial is the address a connection is first attempted to: the proxy
// (with its scheme's default port) or the request's own host.
func expectedDial(t *testing.T, target string, proxy *string) string {
	t.Helper()
	endpoint := target
	defaultPort := map[string]string{"http": "80", "https": "443"}
	if proxy != nil {
		endpoint = *proxy
		defaultPort["socks5"], defaultPort["socks5h"] = "1080", "1080"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	port := parsed.Port()
	if port == "" {
		port = defaultPort[parsed.Scheme]
	}
	return strings.ToLower(net.JoinHostPort(strings.TrimSuffix(parsed.Hostname(), "."), port))
}

func TestForwarderRoutesEverySharedPlannerProfile(t *testing.T) {
	for _, profile := range loadTransportFixture(t).Profiles {
		t.Run(profile.Name, func(t *testing.T) {
			forwarder, log := newRecordingForwarder(t)
			transport := TransportFromWire(&profile.Wire)

			if profile.ConnectTimeoutMS != nil && transport.ConnectTimeout != time.Duration(*profile.ConnectTimeoutMS)*time.Millisecond {
				t.Fatalf("connect timeout = %v, want %dms", transport.ConnectTimeout, *profile.ConnectTimeoutMS)
			}
			if profile.RequestTimeoutMS != nil && transport.RequestTimeout != time.Duration(*profile.RequestTimeoutMS)*time.Millisecond {
				t.Fatalf("request timeout = %v, want %dms", transport.RequestTimeout, *profile.RequestTimeoutMS)
			}
			client, err := forwarder.clientFor(transport)
			if err != nil {
				t.Fatalf("valid Planner profile rejected: %v", err)
			}
			wantTimeout := forwarder.client.Timeout
			if transport.RequestTimeout > 0 {
				wantTimeout = transport.RequestTimeout
			}
			if client.Timeout != wantTimeout {
				t.Fatalf("client timeout = %v, want %v (omitted request timeouts keep the deployment default)", client.Timeout, wantTimeout)
			}

			for _, request := range profile.Requests {
				_, err := forwarder.ForwardStatus(ForwardRequest{TargetURL: request.URL, Method: http.MethodGet, Transport: transport})
				if err == nil {
					t.Fatalf("%s: recording dialer unexpectedly connected", request.URL)
				}
				dialed := log.take()
				if len(dialed) == 0 {
					t.Fatalf("%s: no connection was attempted: %v", request.URL, err)
				}
				if want := expectedDial(t, request.URL, request.Proxy); canonicalDial(dialed[0]) != want {
					t.Errorf("%s first dialed %q, want %q (proxy %v)", request.URL, dialed[0], want, request.Proxy)
				}
			}
		})
	}
}

func TestForwarderRejectsWireTransportsOutsideThePortableProfile(t *testing.T) {
	for _, invalid := range loadTransportFixture(t).InvalidWire {
		t.Run(invalid.Name, func(t *testing.T) {
			forwarder, log := newRecordingForwarder(t)
			_, err := forwarder.ForwardStatus(ForwardRequest{
				TargetURL: "http://hooks.example.test/delivery", Method: http.MethodPost,
				Transport: TransportFromWire(&invalid.Wire),
			})
			if !errors.Is(err, ErrInvalidTransport) {
				t.Fatalf("error = %v, want ErrInvalidTransport", err)
			}
			if dialed := log.take(); len(dialed) != 0 {
				t.Fatalf("an invalid profile still attempted connections: %v", dialed)
			}
		})
	}
}
