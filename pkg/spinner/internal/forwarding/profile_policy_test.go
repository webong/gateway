//go:build !gateway_smoke

package forwarding

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
)

// productionForwarder keeps the production address policy: only public
// addresses are dialed, and that includes the hop to a proxy.
func productionForwarder(t *testing.T) *Forwarder {
	t.Helper()
	forwarder := NewForwarder(&config.Config{
		MaxBodySize: 1024, RequestTimeout: 2 * time.Second, IdleConnTimeout: time.Second,
		MaxIdleConns: 2, MaxConnsPerHost: 2,
	}, logging.NewLogger("error"))
	t.Cleanup(forwarder.CloseIdleConnections)
	return forwarder
}

func TestProxyHopsMustPassTheAddressPolicy(t *testing.T) {
	var httpHits atomic.Int32
	httpProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpHits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer httpProxy.Close()
	socks := startSOCKS(t, "", "", func(string, uint16) string { return httpProxy.Listener.Addr().String() })

	const plain, secure, literal = "http://hooks.example.test/delivery", "https://hooks.example.test/delivery", "http://192.0.2.10:8080/delivery"
	for name, test := range map[string]struct {
		target string
		proxy  HTTPTransport
	}{
		"loopback HTTP proxy":            {plain, HTTPTransport{HTTPProxy: httpProxy.URL}},
		"loopback HTTPS proxy setting":   {secure, HTTPTransport{HTTPSProxy: httpProxy.URL}},
		"loopback SOCKS proxy":           {plain, HTTPTransport{HTTPProxy: socks.URL("socks5h")}},
		"loopback SOCKS proxy local DNS": {literal, HTTPTransport{HTTPProxy: socks.URL("socks5")}},
		"private IPv4 proxy":             {plain, HTTPTransport{HTTPProxy: "http://10.0.0.7:3128"}},
		"metadata-address proxy":         {plain, HTTPTransport{HTTPProxy: "http://169.254.169.254:3128"}},
		"shared-address-space proxy":     {plain, HTTPTransport{HTTPProxy: "socks5h://100.100.100.200:1080"}},
		"IPv6 loopback proxy":            {plain, HTTPTransport{HTTPProxy: "http://[::1]:3128"}},
		"IPv4-mapped loopback proxy":     {plain, HTTPTransport{HTTPProxy: "http://[::ffff:127.0.0.1]:3128"}},
		"hostname resolving to loopback": {plain, HTTPTransport{HTTPProxy: "http://localhost:" + strings.Split(hostPort(t, httpProxy.URL), ":")[1]}},
	} {
		t.Run(name, func(t *testing.T) {
			forwarder := productionForwarder(t)
			test.proxy.ConnectTimeout = time.Second
			_, err := forwarder.ForwardStatus(ForwardRequest{TargetURL: test.target, Method: http.MethodPost, Transport: test.proxy})
			if err == nil {
				t.Fatal("an unauthorized proxy hop was used")
			}
			if !strings.Contains(err.Error(), "blocked addresses") && !strings.Contains(err.Error(), "invalid outbound HTTP transport") {
				t.Fatalf("proxy hop failed for the wrong reason: %v", err)
			}
		})
	}
	if httpHits.Load() != 0 || socks.acceptedConnections() != 0 {
		t.Fatalf("blocked proxies still received traffic: http=%d socks=%d", httpHits.Load(), socks.acceptedConnections())
	}
}

func TestLoopbackTargetsAreDialedDirectlyNotSentToAProxy(t *testing.T) {
	for _, target := range []string{
		"http://127.0.0.1:9/hook", "http://127.0.0.2:9/hook", "http://localhost:9/hook", "http://LOCALHOST.:9/hook",
		"http://[::1]:9/hook", "http://[::ffff:127.0.0.1]:9/hook",
	} {
		t.Run(target, func(t *testing.T) {
			forwarder, log := newRecordingForwarder(t)
			_, _ = forwarder.ForwardStatus(ForwardRequest{
				TargetURL: target, Method: http.MethodPost,
				Transport: HTTPTransport{HTTPProxy: "http://proxy.example.test:3128", HTTPSProxy: "socks5h://proxy.example.test:1080"},
			})
			dialed := log.take()
			if len(dialed) == 0 || strings.Contains(dialed[0], "proxy.example.test") {
				t.Fatalf("loopback target was handed to the proxy: %v", dialed)
			}
		})
	}

	// With the production policy the direct dial is what refuses it, so the
	// error names the target, not the (also blocked) proxy.
	forwarder := productionForwarder(t)
	_, err := forwarder.ForwardStatus(ForwardRequest{
		TargetURL: "http://127.0.0.1:9/hook", Method: http.MethodPost,
		Transport: HTTPTransport{HTTPProxy: "http://10.9.9.9:3128"},
	})
	if err == nil || !strings.Contains(err.Error(), "target 127.0.0.1 resolves only to blocked addresses") {
		t.Fatalf("loopback target with a proxy = %v", err)
	}
}

func TestProfileDeliveriesStillRefuseHostnamesThatResolveToBlockedAddresses(t *testing.T) {
	forwarder := productionForwarder(t)
	for _, transport := range []HTTPTransport{{}, {ConnectTimeout: time.Second}, {NoProxy: []string{"localhost"}}} {
		_, err := forwarder.ForwardStatus(ForwardRequest{TargetURL: "http://localhost:9/hook", Method: http.MethodPost, Transport: transport})
		if err == nil || !strings.Contains(err.Error(), "blocked addresses") {
			t.Fatalf("%+v: localhost delivery = %v", transport, err)
		}
	}
}
