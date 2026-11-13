package forwarding

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webong/gateway/arranger/integration"
	"github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
)

func newTestForwarder(t *testing.T, mutate ...func(*config.Config)) *Forwarder {
	t.Helper()
	settings := &config.Config{
		MaxBodySize: 1024, RequestTimeout: 3 * time.Second, IdleConnTimeout: time.Second,
		MaxIdleConns: 4, MaxConnsPerHost: 4,
	}
	for _, change := range mutate {
		change(settings)
	}
	forwarder := NewForwarder(settings, logging.NewLogger("error"))
	forwarder.addressAllowed = func(net.IP) bool { return true }
	t.Cleanup(forwarder.CloseIdleConnections)
	return forwarder
}

// routeDialer stands in for Gateway's policy dialer: it connects only to the
// addresses a test names, so fake host names reach real loopback servers.
func routeDialer(routes map[string]string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		target, ok := routes[strings.ToLower(address)]
		if !ok {
			return nil, fmt.Errorf("unexpected dial to %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
}

func hostPort(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Host
}

// fakeSOCKS is a SOCKS5 CONNECT server that records what the client asked for.
type fakeSOCKS struct {
	listener net.Listener
	user     string
	password string
	route    func(host string, port uint16) string

	mu       sync.Mutex
	accepted int
	requests []socksRequest
}

type socksRequest struct {
	addressType byte
	host        string
	port        uint16
	user        string
	password    string
}

func startSOCKS(t *testing.T, user, password string, route func(string, uint16) string) *fakeSOCKS {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeSOCKS{listener: listener, user: user, password: password, route: route}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			server.mu.Lock()
			server.accepted++
			server.mu.Unlock()
			go server.handle(connection)
		}
	}()
	return server
}

func (s *fakeSOCKS) URL(scheme string) string {
	credentials := ""
	if s.user != "" {
		credentials = url.UserPassword(s.user, s.password).String() + "@"
	}
	return scheme + "://" + credentials + s.listener.Addr().String()
}

func (s *fakeSOCKS) handle(connection net.Conn) {
	defer connection.Close()
	readByte := func() (byte, bool) {
		var b [1]byte
		_, err := io.ReadFull(connection, b[:])
		return b[0], err == nil
	}
	readN := func(n int) ([]byte, bool) {
		buffer := make([]byte, n)
		_, err := io.ReadFull(connection, buffer)
		return buffer, err == nil
	}
	header, ok := readN(2)
	if !ok || header[0] != 5 {
		return
	}
	methods, ok := readN(int(header[1]))
	if !ok {
		return
	}
	request := socksRequest{}
	if s.user != "" {
		if !bytes.Contains(methods, []byte{2}) {
			connection.Write([]byte{5, 0xff})
			return
		}
		connection.Write([]byte{5, 2})
		if _, ok := readByte(); !ok {
			return
		}
		length, _ := readByte()
		user, _ := readN(int(length))
		length, _ = readByte()
		password, _ := readN(int(length))
		request.user, request.password = string(user), string(password)
		if request.user != s.user || request.password != s.password {
			connection.Write([]byte{1, 1})
			return
		}
		connection.Write([]byte{1, 0})
	} else {
		if !bytes.Contains(methods, []byte{0}) {
			connection.Write([]byte{5, 0xff})
			return
		}
		connection.Write([]byte{5, 0})
	}
	command, ok := readN(4)
	if !ok || command[1] != 1 {
		return
	}
	request.addressType = command[3]
	switch command[3] {
	case 1:
		address, _ := readN(4)
		request.host = net.IP(address).String()
	case 3:
		length, _ := readByte()
		name, _ := readN(int(length))
		request.host = string(name)
	case 4:
		address, _ := readN(16)
		request.host = net.IP(address).String()
	}
	port, _ := readN(2)
	request.port = uint16(port[0])<<8 | uint16(port[1])
	s.mu.Lock()
	s.requests = append(s.requests, request)
	s.mu.Unlock()

	upstream, err := net.Dial("tcp", s.route(request.host, request.port))
	if err != nil {
		connection.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer upstream.Close()
	connection.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	done := make(chan struct{})
	go func() { io.Copy(upstream, connection); close(done) }()
	io.Copy(connection, upstream)
	<-done
}

func (s *fakeSOCKS) seen() []socksRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]socksRequest(nil), s.requests...)
}

func (s *fakeSOCKS) acceptedConnections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepted
}

// connectProxy is an HTTP proxy that tunnels CONNECT to a routed address.
func connectProxy(t *testing.T, route func(authority string) string, tunnels *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.Dial("tcp", route(r.Host))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hijacker := w.(http.Hijacker)
		client, _, err := hijacker.Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		tunnels.Add(1)
		client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() { io.Copy(upstream, client); upstream.Close() }()
		go func() { io.Copy(client, upstream); client.Close() }()
	}))
	t.Cleanup(server.Close)
	return server
}

func TestSOCKSProfilesCarryDNSModesAndAuthentication(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Errorf("SOCKS credentials reached the subscriber: %v", r.Header)
		}
		if r.Header.Get("X-Gateway-Route-Id") != "route-1" {
			t.Errorf("request headers were lost: %v", r.Header)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()
	route := func(string, uint16) string { return target.Listener.Addr().String() }

	for _, test := range []struct {
		name        string
		scheme      string
		user        string
		targetURL   string
		addressType byte
		host        string
	}{
		{"remote DNS", "socks5h", "", "http://subscriber.example.test/hook", 3, "subscriber.example.test"},
		{"remote DNS with credentials", "socks5h", "gateway", "http://subscriber.example.test/hook", 3, "subscriber.example.test"},
		{"local DNS sends the resolved address", "socks5", "", "http://192.0.2.10:8080/hook", 1, "192.0.2.10"},
		{"local DNS with credentials", "socks5", "gateway", "http://192.0.2.10:8080/hook", 1, "192.0.2.10"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socks := startSOCKS(t, test.user, "s3cret", route)
			forwarder := newTestForwarder(t)
			status, err := forwarder.ForwardStatus(ForwardRequest{
				TargetURL: test.targetURL, Method: http.MethodPost,
				Headers:   map[string][]string{"X-Gateway-Route-Id": {"route-1"}},
				Transport: HTTPTransport{HTTPProxy: socks.URL(test.scheme), HTTPSProxy: socks.URL(test.scheme), ConnectTimeout: time.Second, RequestTimeout: 2 * time.Second},
			})
			if err != nil || status != http.StatusAccepted {
				t.Fatalf("delivery through SOCKS = %d, %v", status, err)
			}
			seen := socks.seen()
			if len(seen) != 1 || seen[0].addressType != test.addressType || seen[0].host != test.host {
				t.Fatalf("SOCKS server saw %+v, want address type %d for %s", seen, test.addressType, test.host)
			}
			if test.user != "" && (seen[0].user != test.user || seen[0].password != "s3cret") {
				t.Fatalf("SOCKS credentials = %q/%q", seen[0].user, seen[0].password)
			}
		})
	}
}

func TestSOCKSProfileWithWrongCredentialsFailsWithoutReachingTheTarget(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Add(1) }))
	defer target.Close()
	socks := startSOCKS(t, "gateway", "right", func(string, uint16) string { return target.Listener.Addr().String() })
	forwarder := newTestForwarder(t)
	wrong := strings.Replace(socks.URL("socks5h"), "right", "wrong", 1)

	_, err := forwarder.ForwardStatus(ForwardRequest{
		TargetURL: "http://subscriber.example.test/hook", Method: http.MethodPost,
		Transport: HTTPTransport{HTTPProxy: wrong, ConnectTimeout: time.Second},
	})
	if err == nil || reached.Load() != 0 {
		t.Fatalf("rejected SOCKS credentials delivered anyway: err=%v reached=%d", err, reached.Load())
	}
}

func TestHTTPSTargetsThroughProxiesVerifyTLSWithGatewayTrust(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()
	pool := x509.NewCertPool()
	pool.AddCert(target.Certificate())

	var tunnels atomic.Int32
	httpProxy := connectProxy(t, func(string) string { return target.Listener.Addr().String() }, &tunnels)
	socks := startSOCKS(t, "", "", func(string, uint16) string { return target.Listener.Addr().String() })

	for name, transport := range map[string]HTTPTransport{
		"HTTP CONNECT proxy": {HTTPSProxy: httpProxy.URL},
		"SOCKS5 proxy":       {HTTPSProxy: socks.URL("socks5h")},
		"no proxy":           {ConnectTimeout: time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			untrusted := newTestForwarder(t)
			if name == "no proxy" {
				untrusted.transport.DialContext = routeDialer(map[string]string{"example.com:443": target.Listener.Addr().String()})
			}
			if _, err := untrusted.ForwardStatus(ForwardRequest{TargetURL: "https://example.com/hook", Method: http.MethodPost, Transport: transport}); err == nil {
				t.Fatal("an untrusted TLS certificate was accepted")
			} else if !strings.Contains(err.Error(), "certificate") {
				t.Fatalf("expected a certificate error, got %v", err)
			}

			trusted := newTestForwarder(t)
			// Gateway's own TLS settings survive client construction.
			trusted.transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
			if name == "no proxy" {
				trusted.transport.DialContext = routeDialer(map[string]string{"example.com:443": target.Listener.Addr().String()})
			}
			status, err := trusted.ForwardStatus(ForwardRequest{TargetURL: "https://example.com/hook", Method: http.MethodPost, Transport: transport})
			if err != nil || status != http.StatusAccepted {
				t.Fatalf("trusted TLS delivery = %d, %v", status, err)
			}
		})
	}
	// Trust is decided end to end inside the tunnel, so the proxy carried both the
	// rejected and the accepted attempt.
	if tunnels.Load() != 2 {
		t.Fatalf("HTTP CONNECT proxy tunnelled %d times, want 2", tunnels.Load())
	}
}

func TestProxyAuthenticationReachesOnlyTheProxyHop(t *testing.T) {
	var mu sync.Mutex
	var proxied, direct []http.Header
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		proxied = append(proxied, r.Header.Clone())
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer proxy.Close()
	subscriber := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		direct = append(direct, r.Header.Clone())
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer subscriber.Close()

	forwarder := newTestForwarder(t)
	forwarder.transport.DialContext = routeDialer(map[string]string{
		"proxy.example.test:8080":        proxy.Listener.Addr().String(),
		"hooks.internal.example.test:80": subscriber.Listener.Addr().String(),
	})
	transport := HTTPTransport{
		HTTPProxy: "http://gateway:p%40ss@proxy.example.test:8080",
		NoProxy:   []string{".internal.example.test"},
	}
	headers := map[string][]string{"Authorization": {"Bearer subscriber-token"}}
	for _, target := range []string{"http://hooks.example.test/delivery", "http://hooks.internal.example.test/delivery"} {
		if status, err := forwarder.ForwardStatus(ForwardRequest{TargetURL: target, Method: http.MethodPost, Headers: headers, Transport: transport}); err != nil || status != http.StatusAccepted {
			t.Fatalf("%s = %d, %v", target, status, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(proxied) != 1 || len(direct) != 1 {
		t.Fatalf("proxied %d, direct %d; want 1 each", len(proxied), len(direct))
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("gateway:p@ss"))
	if proxied[0].Get("Proxy-Authorization") != wantAuth {
		t.Fatalf("proxy credentials = %q, want %q", proxied[0].Get("Proxy-Authorization"), wantAuth)
	}
	if direct[0].Get("Proxy-Authorization") != "" {
		t.Fatalf("proxy credentials leaked to a bypassed destination: %v", direct[0])
	}
	for _, header := range []http.Header{proxied[0], direct[0]} {
		if header.Get("Authorization") != "Bearer subscriber-token" {
			t.Fatalf("subscriber credentials were not carried: %v", header)
		}
	}
}

func TestEnvironmentProxyVariablesNeverRouteDeliveries(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, "http://environment-proxy.invalid:3128")
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	for name, transport := range map[string]HTTPTransport{
		"shared client":            {},
		"timeouts only":            {ConnectTimeout: time.Second, RequestTimeout: 2 * time.Second},
		"no-proxy rules only":      {NoProxy: []string{".internal.example.test"}},
		"explicit different proxy": {HTTPProxy: "http://configured-proxy.example.test:3128"},
	} {
		t.Run(name, func(t *testing.T) {
			forwarder, log := newRecordingForwarder(t)
			_, _ = forwarder.ForwardStatus(ForwardRequest{TargetURL: "http://hooks.example.test/delivery", Method: http.MethodPost, Transport: transport})
			dialed := log.take()
			want := "hooks.example.test:80"
			if transport.HTTPProxy != "" {
				want = "configured-proxy.example.test:3128"
			}
			if len(dialed) == 0 || dialed[0] != want {
				t.Fatalf("first connection = %v, want %s", dialed, want)
			}
		})
	}
}

func TestProfileConnectDeadlineOnlyCapsTheDial(t *testing.T) {
	forwarder, _ := newRecordingForwarder(t)
	var remaining []time.Duration
	var mu sync.Mutex
	forwarder.transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		deadline, ok := ctx.Deadline()
		mu.Lock()
		defer mu.Unlock()
		if ok {
			remaining = append(remaining, time.Until(deadline))
		} else {
			remaining = append(remaining, -1)
		}
		return nil, errors.New("dial recorded")
	}

	for _, transport := range []HTTPTransport{{ConnectTimeout: 250 * time.Millisecond}, {HTTPProxy: "http://proxy.example.test:3128"}} {
		_, _ = forwarder.ForwardStatus(ForwardRequest{TargetURL: "http://hooks.example.test/delivery", Method: http.MethodPost, Transport: transport})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(remaining) != 2 {
		t.Fatalf("dials = %v", remaining)
	}
	if remaining[0] <= 0 || remaining[0] > 250*time.Millisecond {
		t.Fatalf("connect budget = %v, want within 250ms", remaining[0])
	}
	if remaining[1] != -1 && remaining[1] < time.Second {
		t.Fatalf("a profile without a connect timeout capped the dial at %v", remaining[1])
	}
}

func TestProfileClientsHonorRequestTimeoutAndCancellation(t *testing.T) {
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	forwarder := newTestForwarder(t)

	started := time.Now()
	_, err := forwarder.ForwardStatus(ForwardRequest{
		TargetURL: slow.URL, Method: http.MethodPost,
		Transport: HTTPTransport{RequestTimeout: 150 * time.Millisecond},
	})
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() || time.Since(started) > 2*time.Second {
		t.Fatalf("request timeout = %v after %v", err, time.Since(started))
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	started = time.Now()
	_, err = forwarder.ForwardStatus(ForwardRequest{
		TargetURL: slow.URL, Method: http.MethodPost, Context: ctx,
		Transport: HTTPTransport{RequestTimeout: 30 * time.Second},
	})
	if !errors.Is(err, context.Canceled) || time.Since(started) > 2*time.Second {
		t.Fatalf("cancellation = %v after %v", err, time.Since(started))
	}
}

func TestProfileClientsKeepTheResponseBodyLimit(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 4096))
	}))
	defer target.Close()
	forwarder := newTestForwarder(t, func(settings *config.Config) { settings.MaxBodySize = 16 })

	_, err := forwarder.ForwardSync(ForwardRequest{TargetURL: target.URL, Method: http.MethodGet, Transport: HTTPTransport{ConnectTimeout: time.Second}})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("profile client returned %v, want ErrResponseTooLarge", err)
	}
}

func TestProfileClientsAreReusedBoundedAndReleaseIdleConnections(t *testing.T) {
	var opened, closed atomic.Int32
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	target.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			opened.Add(1)
		case http.StateClosed:
			closed.Add(1)
		}
	}
	target.Start()
	defer target.Close()
	forwarder := newTestForwarder(t)

	first := HTTPTransport{ConnectTimeout: time.Second}
	client, err := forwarder.clientFor(first)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := forwarder.clientFor(first); again != client {
		t.Fatal("a repeated profile did not reuse its client")
	}
	if status, err := forwarder.ForwardStatus(ForwardRequest{TargetURL: target.URL, Method: http.MethodGet, Transport: first}); err != nil || status != http.StatusNoContent {
		t.Fatalf("delivery = %d, %v", status, err)
	}
	if opened.Load() != 1 || closed.Load() != 0 {
		t.Fatalf("expected one idle keep-alive connection, got opened=%d closed=%d", opened.Load(), closed.Load())
	}

	for i := 1; i <= maxTransportClients; i++ {
		if _, err := forwarder.clientFor(HTTPTransport{ConnectTimeout: time.Second + time.Duration(i)*time.Millisecond}); err != nil {
			t.Fatal(err)
		}
	}
	if len(forwarder.clients) != maxTransportClients || len(forwarder.clientOrder) != maxTransportClients {
		t.Fatalf("cache holds %d clients (%d ordered), want %d", len(forwarder.clients), len(forwarder.clientOrder), maxTransportClients)
	}
	if replaced, _ := forwarder.clientFor(first); replaced == client {
		t.Fatal("the oldest client was not evicted")
	}
	waitFor(t, "evicted client to release its idle connection", func() bool { return closed.Load() == 1 })

	forwarder.CloseIdleConnections()
	if _, err := forwarder.ForwardStatus(ForwardRequest{TargetURL: target.URL, Method: http.MethodGet, Transport: HTTPTransport{ConnectTimeout: 2 * time.Second}}); err != nil {
		t.Fatal(err)
	}
	forwarder.CloseIdleConnections()
	waitFor(t, "forwarder shutdown to release idle connections", func() bool { return closed.Load() == opened.Load() })
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestProfileClientsFollowRedirectsOnlyThroughTheAddressPolicy(t *testing.T) {
	ipv6, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback: %v", err)
	}
	var blockedHits atomic.Int32
	blocked := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		blockedHits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	blocked.Listener.Close()
	blocked.Listener = ipv6
	blocked.Start()
	defer blocked.Close()
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer allowed.Close()

	redirect := "/to-allowed"
	var redirectTarget atomic.Value
	redirectTarget.Store(allowed.URL)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.Load().(string)+redirect, http.StatusFound)
	}))
	defer origin.Close()

	forwarder := newTestForwarder(t)
	forwarder.addressAllowed = func(ip net.IP) bool { return ip.To4() != nil }
	transport := HTTPTransport{ConnectTimeout: time.Second, RequestTimeout: 2 * time.Second}

	status, err := forwarder.ForwardStatus(ForwardRequest{TargetURL: origin.URL, Method: http.MethodGet, Transport: transport})
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("redirect to an allowed address = %d, %v", status, err)
	}

	redirectTarget.Store(blocked.URL)
	_, err = forwarder.ForwardStatus(ForwardRequest{TargetURL: origin.URL, Method: http.MethodGet, Transport: transport})
	if err == nil || !strings.Contains(err.Error(), "blocked addresses") || blockedHits.Load() != 0 {
		t.Fatalf("redirect to a blocked IPv6 address = %v (hits %d)", err, blockedHits.Load())
	}
}

func TestPrivateCallbacksRejectEveryProfileProxyFieldAndNeverFollowRedirects(t *testing.T) {
	var followed atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { followed.Add(1) }))
	defer elsewhere.Close()
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer callback.Close()
	forwarder := newTestForwarder(t)
	forwarder.privateCallbackNetworks = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	request := ForwardRequest{TargetURL: callback.URL, Method: http.MethodPost}
	request.Transport.PrivateCallbackCIDRs = "127.0.0.0/8"

	status, err := forwarder.ForwardStatus(request)
	if err != nil || status != http.StatusFound || followed.Load() != 0 {
		t.Fatalf("private callback = %d, %v (redirect followed %d times)", status, err, followed.Load())
	}
	for name, settings := range map[string]HTTPTransport{
		"http proxy":  {HTTPProxy: "http://proxy.example.test:8080"},
		"https proxy": {HTTPSProxy: "socks5h://proxy.example.test:1080"},
		"no-proxy":    {NoProxy: []string{".internal.example.test"}},
	} {
		settings.PrivateCallbackCIDRs = "127.0.0.0/8"
		request.Transport = settings
		if _, err := forwarder.ForwardStatus(request); err == nil || !strings.Contains(err.Error(), "private callback cannot use an HTTP proxy") {
			t.Fatalf("%s: private callback accepted profile proxy settings: %v", name, err)
		}
	}
}

func TestInvalidTransportErrorsAreTypedAndNeverRepeatCredentials(t *testing.T) {
	forwarder, log := newRecordingForwarder(t)
	_, err := forwarder.ForwardStatus(ForwardRequest{
		TargetURL: "http://hooks.example.test/delivery", Method: http.MethodPost,
		Transport: HTTPTransport{HTTPProxy: "http://gateway:proxy-secret@proxy.example.test:8080/relay?token=1"},
	})
	if !errors.Is(err, ErrInvalidTransport) {
		t.Fatalf("error = %v, want ErrInvalidTransport", err)
	}
	if strings.Contains(err.Error(), "proxy-secret") {
		t.Fatalf("rejection repeated the proxy credential: %v", err)
	}
	if dialed := log.take(); len(dialed) != 0 {
		t.Fatalf("an invalid profile attempted connections: %v", dialed)
	}
}

// The Planner serializes the delivery it plans; this test routes exactly that
// JSON. tests/Feature/EndpointHttpTransportResolverTest.php writes the file and
// runs this test, so a change to either side of the wire fails the PHP suite.
func TestPlannerSerializedDeliveryIsForwardedThroughItsProfile(t *testing.T) {
	path := os.Getenv("GATEWAY_PLANNER_DELIVERY_JSON")
	if path == "" {
		t.Skip("GATEWAY_PLANNER_DELIVERY_JSON is set by the PHP Planner test")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var delivery integration.Delivery
	if err := json.Unmarshal(raw, &delivery); err != nil {
		t.Fatalf("decode Planner delivery: %v", err)
	}
	if err := delivery.Validate(); err != nil {
		t.Fatalf("Planner delivery is not valid wire: %v", err)
	}
	if delivery.Transport == nil || delivery.Transport.Proxy == nil || delivery.Transport.Proxy.HTTPS != "http://gateway:p%40ss@proxy.example.test:8080" {
		t.Fatalf("the Planner did not serialize the profile's proxy: %+v", delivery.Transport)
	}

	type received struct {
		method, path, body string
		header             http.Header
	}
	got := make(chan received, 1)
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- received{r.Method, r.URL.Path, string(body), r.Header.Clone()}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()
	pool := x509.NewCertPool()
	pool.AddCert(target.Certificate())

	var tunnels atomic.Int32
	var proxyAuth atomic.Value
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyAuth.Store(r.Header.Get("Proxy-Authorization"))
		upstream, err := net.Dial("tcp", target.Listener.Addr().String())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		tunnels.Add(1)
		client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() { io.Copy(upstream, client); upstream.Close() }()
		go func() { io.Copy(client, upstream); client.Close() }()
	}))
	defer proxy.Close()

	forwarder := newTestForwarder(t)
	forwarder.transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	forwarder.transport.DialContext = routeDialer(map[string]string{"proxy.example.test:8080": proxy.Listener.Addr().String()})

	status, err := forwarder.ForwardStatus(ForwardRequest{
		TargetURL: delivery.URL, RequestBody: delivery.Body, Method: delivery.Method, RawQuery: delivery.RawQuery,
		Headers: delivery.Headers, Transport: TransportFromWire(delivery.Transport),
	})
	if err != nil || status != http.StatusAccepted {
		t.Fatalf("forwarding the Planner's delivery = %d, %v", status, err)
	}
	request := <-got
	if tunnels.Load() != 1 || request.method != delivery.Method || string(delivery.Body) != request.body || request.header.Get("Proxy-Authorization") != "" {
		t.Fatalf("delivery arrived as %+v after %d tunnels", request, tunnels.Load())
	}
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("gateway:p@ss"))
	if proxyAuth.Load() != wantAuth {
		t.Fatalf("proxy authentication = %v", proxyAuth.Load())
	}
}
