package router

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	arrangerrouter "github.com/webong/gateway/arranger/integration"
	"github.com/webong/gateway/pkg/spinner/transport"
)

func TestRunServesBundledTLSAndIMAP(t *testing.T) {
	certFile, keyFile := testTLSFiles(t)
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_ROUTER_APP_DIR", t.TempDir())
	t.Setenv("GATEWAY_ROUTER_LISTEN", "127.0.0.1:0")
	httpsAddr := unusedLoopbackAddress(t)
	imapAddr := unusedLoopbackAddress(t)
	t.Setenv("GATEWAY_ROUTER_TLS_LISTEN", httpsAddr)
	t.Setenv("GATEWAY_ROUTER_TLS_CERT_FILE", certFile)
	t.Setenv("GATEWAY_ROUTER_TLS_KEY_FILE", keyFile)
	t.Setenv("GATEWAY_IMAP_ADDR", imapAddr)
	t.Setenv("GATEWAY_IMAP_TLS_CERT_FILE", certFile)
	t.Setenv("GATEWAY_IMAP_TLS_KEY_FILE", keyFile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, func(string) (Planner, error) { return &controlTestPlanner{}, nil }) }()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	deadline := time.Now().Add(3 * time.Second)
	for {
		response, err := client.Get("https://" + httpsAddr + "/health")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("HTTPS returned %d", response.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bundled HTTPS listener unavailable: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", imapAddr,
		&tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("bundled IMAP listener unavailable: %v", err)
	}
	buffer := make([]byte, 512)
	n, err := connection.Read(buffer)
	_ = connection.Close()
	if err != nil || !strings.Contains(string(buffer[:n]), "IMAP") {
		t.Fatalf("bundled IMAP greeting = %q, err = %v", buffer[:n], err)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bundled Router did not stop")
	}
}

func testTLSFiles(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gateway"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"gateway"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "gateway"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true,
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}

func TestRunOwnsPlannerLifecycle(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_ROUTER_APP_DIR", t.TempDir())
	t.Setenv("GATEWAY_ROUTER_LISTEN", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	planner := &testPlanner{}
	err := Run(ctx, func(appDir string) (Planner, error) {
		if appDir == "" {
			t.Fatal("missing app directory")
		}
		cancel()
		return planner, nil
	})
	if err != nil || !planner.closed {
		t.Fatalf("Run returned %v; Planner closed = %v", err, planner.closed)
	}
}

func TestRunReturnsPlannerStartupError(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_ROUTER_APP_DIR", t.TempDir())
	want := errors.New("PHP startup failed")
	if err := Run(context.Background(), func(string) (Planner, error) { return nil, want }); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestHandlerRoundTripperCreatesACompleteServerRequest(t *testing.T) {
	transport := handlerRoundTripper{handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Body == nil || request.RequestURI != "/_internal/gateway/extensions?active=1" || request.RemoteAddr == "" {
			t.Errorf("incomplete server request: body=%v requestURI=%q remoteAddr=%q", request.Body, request.RequestURI, request.RemoteAddr)
		}
		writer.WriteHeader(http.StatusNoContent)
	})}
	request, err := http.NewRequest(http.MethodGet, "http://gateway.internal/_internal/gateway/extensions?active=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestRunPollsSchedulesThroughEmbeddedPlanner(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_SCHEDULES_ENABLED", "true")
	t.Setenv("GATEWAY_ROUTER_APP_DIR", t.TempDir())
	t.Setenv("GATEWAY_ROUTER_LISTEN", "127.0.0.1:0")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	planner := &schedulePlanner{claimed: make(chan string, 1), cancel: cancel}
	if err := Run(ctx, func(string) (Planner, error) { return planner, nil }); err != nil {
		t.Fatal(err)
	}
	if !planner.closed {
		t.Fatal("embedded Planner was not closed")
	}
	select {
	case token := <-planner.claimed:
		if token != "test-token" {
			t.Fatalf("embedded schedule claim had token %q", token)
		}
	default:
		t.Fatal("bundled Router did not poll the embedded Planner")
	}
}

type testPlanner struct{ closed bool }

func (*testPlanner) ServeHTTP(http.ResponseWriter, *http.Request) {}
func (p *testPlanner) Close()                                     { p.closed = true }

type schedulePlanner struct {
	claimed chan string
	cancel  context.CancelFunc
	closed  bool
}

func (p *schedulePlanner) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/_internal/gateway/schedules/claim" {
		p.claimed <- request.Header.Get("X-Gateway-Internal")
		p.cancel()
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write([]byte(`{"data":[]}`))
}

func (p *schedulePlanner) Close() { p.closed = true }

func TestRouterHandlerRoutesOnlyHooksToSpinner(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/hooks/order", want: "edge"},
		{path: "/hooks2/order", want: "planner"},
		{path: "/registry/endpoints", want: "planner"},
		{path: "/email/worker-ingress/route-1", want: "planner"},
		{path: "/_internal/gateway/plan", want: "404 page not found\n"},
	} {
		t.Run(test.path, func(t *testing.T) {
			planner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("planner")) })
			edge := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("edge")) })
			response := httptest.NewRecorder()
			NewHandler(planner, edge, edge, nil, nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Body.String() != test.want {
				t.Fatalf("got %q, want %q", response.Body.String(), test.want)
			}
		})
	}
}

func TestDisabledManagedRouteIsNilAtRouterHandler(t *testing.T) {
	managed, err := ManagedRouteProxy(&transport.Router{})
	if err != nil || managed != nil {
		t.Fatalf("disabled managed route = %v, err=%v", managed, err)
	}
	planner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("healthy")) })
	response := httptest.NewRecorder()
	NewHandler(planner, planner, planner, nil, managed).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusOK || response.Body.String() != "healthy" {
		t.Fatalf("default Router returned %d %q", response.Code, response.Body.String())
	}
}

func TestInternalControlOnlyForwardsCapabilityOwnedRoutes(t *testing.T) {
	planner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Internal") != "worker-secret" || r.Header.Get("X-Gateway-Target") != "target-1" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(r.URL.Path))
	})
	extensionPolicy := func(request *http.Request) bool {
		return request.Method == http.MethodPost && request.URL.Path == "/_internal/example/action"
	}
	control := arrangerrouter.NewInternalControlHandler(planner, InternalRequestPolicy(extensionPolicy))
	for _, test := range []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: "/_internal/provisioning/targets/target-1", want: http.StatusOK},
		{method: http.MethodPost, path: "/_internal/example/action", want: http.StatusOK},
		{method: http.MethodGet, path: "/_internal/example/action", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/_internal/provisioning2/targets/target-1", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/_internal/gateway/plan", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/registry/endpoints", want: http.StatusNotFound},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("X-Gateway-Internal", "worker-secret")
		request.Header.Set("X-Gateway-Target", "target-1")
		response := httptest.NewRecorder()
		control.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("%s %s returned %d, want %d", test.method, test.path, response.Code, test.want)
		}
	}
	response := httptest.NewRecorder()
	control.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/_internal/provisioning/targets/target-1", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("Planner authentication was bypassed: %d", response.Code)
	}
	response = httptest.NewRecorder()
	control.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/_internal/example/action", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("extension policy accepted the wrong method: %d", response.Code)
	}
}

func TestRouterHandlerMountsHostProvidedPublicRoutes(t *testing.T) {
	planner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("planner")) })
	extension := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("extension")) })
	handler := NewHandler(planner, planner, nil, nil, nil, arrangerrouter.PublicRoute{
		Prefix:  "/extension",
		Handler: extension,
	})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/extension/resource", nil))
	if response.Code != http.StatusOK || response.Body.String() != "extension" {
		t.Fatalf("host-provided route returned %d %q", response.Code, response.Body.String())
	}
}

func TestRunServesPrivatePlannerControl(t *testing.T) {
	t.Setenv("GATEWAY_RUNTIME", "")
	t.Setenv("GATEWAY_INTERNAL_TOKEN", "test-token")
	t.Setenv("GATEWAY_ROUTER_APP_DIR", t.TempDir())
	t.Setenv("GATEWAY_ROUTER_LISTEN", "127.0.0.1:0")
	t.Setenv("GATEWAY_ROUTER_CONTROL_LISTEN", unusedLoopbackAddress(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- Run(ctx, func(string) (Planner, error) { return &controlTestPlanner{}, nil })
	}()
	controlURL := "http://" + os.Getenv("GATEWAY_ROUTER_CONTROL_LISTEN")
	response := awaitControlResponse(t, controlURL+"/_internal/provisioning/targets/target-1")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("private Planner route returned %d", response.StatusCode)
	}
	_ = response.Body.Close()
	response = awaitControlResponse(t, controlURL+"/registry/endpoints")
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("private control exposed public Planner route: %d", response.StatusCode)
	}
	_ = response.Body.Close()
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("bundled Router did not stop")
	}
}

type controlTestPlanner struct{}

func (*controlTestPlanner) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}
func (*controlTestPlanner) Close() {}

func unusedLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func awaitControlResponse(t *testing.T, address string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(3 * time.Second)
	for {
		response, err := client.Get(address)
		if err == nil {
			return response
		}
		if time.Now().After(deadline) {
			t.Fatalf("private control listener unavailable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type testManagedRoute struct{ calls int }

func (m *testManagedRoute) ServeWithPlanner(w http.ResponseWriter, r *http.Request, _ http.Handler) bool {
	m.calls++
	if r.URL.Path != "/connection/websocket" {
		return false
	}
	_, _ = w.Write([]byte("managed"))
	return true
}

func TestRouterHandlerRoutesManagedWorkloadsBeforeLaravelAndHooks(t *testing.T) {
	planner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("planner")) })
	edge := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("edge")) })
	managed := &testManagedRoute{}
	handler := NewHandler(planner, edge, edge, nil, managed)
	for path, want := range map[string]string{
		"/connection/websocket": "managed",
		"/hooks/order":          "edge",
		"/registry/endpoints":   "planner",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Body.String() != want {
			t.Errorf("%s returned %q, want %q", path, response.Body.String(), want)
		}
	}
	before := managed.calls
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/_internal/provisioning/routes/resolve", nil))
	if response.Code != http.StatusNotFound || managed.calls != before {
		t.Fatal("public internal route reached managed lookup")
	}
}
