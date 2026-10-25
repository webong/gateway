package pop3

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	bridge "github.com/webong/gateway/arranger/integration"
)

type plannerFunc func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error)

func (f plannerFunc) PlanEvent(ctx context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
	return f(ctx, event)
}

func testTLS(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}, MinVersion: tls.VersionTLS12}, pool
}

func expectLine(t *testing.T, reader *bufio.Reader, expected string) {
	t.Helper()
	line, err := reader.ReadString('\n')
	if err != nil || line != expected+"\r\n" {
		t.Fatalf("line = %q, err = %v; want %q", line, err, expected)
	}
}

func TestTLSProxyAuthenticatesAndStreamsExternalMailbox(t *testing.T) {
	tlsConfig, roots := testTLS(t)
	upstreamListener, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer upstreamListener.Close()
	upstreamDone := make(chan struct{})
	go func() {
		defer close(upstreamDone)
		conn, err := upstreamListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("+OK external mailbox\r\n"))
		if line, _ := reader.ReadString('\n'); line != "USER upstream-alice\r\n" {
			t.Errorf("upstream USER = %q", line)
			return
		}
		_, _ = conn.Write([]byte("+OK\r\n"))
		if line, _ := reader.ReadString('\n'); line != "PASS upstream-secret\r\n" {
			t.Errorf("upstream PASS = %q", line)
			return
		}
		_, _ = conn.Write([]byte("+OK\r\n"))
		if line, _ := reader.ReadString('\n'); line != "STAT\r\n" {
			t.Errorf("upstream command = %q", line)
			return
		}
		_, _ = conn.Write([]byte("+OK 1 42\r\n"))
		if line, _ := reader.ReadString('\n'); line != "QUIT\r\n" {
			t.Errorf("upstream QUIT = %q", line)
			return
		}
		_, _ = conn.Write([]byte("+OK bye\r\n"))
	}()

	var mu sync.Mutex
	var kinds []bridge.EventKind
	planner := plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		mu.Lock()
		kinds = append(kinds, event.Kind)
		mu.Unlock()
		decision := bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolPOP3, Action: bridge.GatewayAccept}
		if event.Kind == bridge.EventAuthenticate {
			if event.Attributes["username"] != "alice@example.test" || event.Attributes["password"] != "client-secret" {
				t.Errorf("unexpected client credentials")
			}
			decision.Metadata = map[string]string{"identity": "account-1", "upstream_id": "primary", "upstream_username": "upstream-alice", "upstream_password": "upstream-secret",
				"upstream_address": upstreamListener.Addr().String(), "upstream_server_name": "localhost", "upstream_network_scope": "private"}
		}
		return decision, nil
	})
	server, err := NewServer(Config{
		Address: "127.0.0.1:0", Hostname: "localhost", TLSConfig: tlsConfig,
		UpstreamRootCAs: roots,
		DialManagedUpstream: func(ctx context.Context, address, serverName, _ string, timeout time.Duration, roots *x509.CertPool) (net.Conn, error) {
			return (&tls.Dialer{NetDialer: &net.Dialer{Timeout: timeout}, Config: &tls.Config{ServerName: serverName, RootCAs: roots, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", address)
		},
	}, planner)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()); <-served }()
	client, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(client)
	expectLine(t, reader, "+OK Gateway POP3 ready")
	for _, exchange := range [][2]string{{"USER alice@example.test", "+OK username received"}, {"PASS client-secret", "+OK mailbox ready"}, {"STAT", "+OK 1 42"}, {"QUIT", "+OK bye"}} {
		if _, err := client.Write([]byte(exchange[0] + "\r\n")); err != nil {
			t.Fatal(err)
		}
		expectLine(t, reader, exchange[1])
	}
	_ = client.Close()
	select {
	case <-upstreamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream session did not finish")
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		got := append([]bridge.EventKind(nil), kinds...)
		mu.Unlock()
		if len(got) == 3 {
			if got[0] != bridge.EventAuthenticate || got[1] != bridge.EventConnect || got[2] != bridge.EventClose {
				t.Fatalf("event kinds = %v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("event kinds = %v", got)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRejectsUnregisteredUpstreamBeforeDial(t *testing.T) {
	tlsConfig, roots := testTLS(t)
	planner := plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolPOP3, Action: bridge.GatewayAccept,
			Metadata: map[string]string{"identity": "account-1", "upstream_id": "not-registered", "upstream_username": "alice", "upstream_password": "secret"}}, nil
	})
	server, err := NewServer(Config{Address: "127.0.0.1:0", TLSConfig: tlsConfig,
		Upstreams: map[string]Upstream{"primary": {Address: "127.0.0.1:995", ServerName: "localhost"}}}, planner)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()); <-served }()
	client, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(client)
	expectLine(t, reader, "+OK Gateway POP3 ready")
	_, _ = client.Write([]byte("USER alice\r\n"))
	expectLine(t, reader, "+OK username received")
	_, _ = client.Write([]byte("PASS secret\r\n"))
	expectLine(t, reader, "-ERR mailbox service unavailable")
}

func TestServerRequiresTLSAndValidStaticUpstreams(t *testing.T) {
	tlsConfig, _ := testTLS(t)
	for _, config := range []Config{{Address: "127.0.0.1:995"},
		{Address: "127.0.0.1:995", TLSConfig: tlsConfig, Upstreams: map[string]Upstream{"bad": {Address: "example.test:995"}}}} {
		if _, err := NewServer(config, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			return bridge.GatewayDecision{}, nil
		})); err == nil {
			t.Fatal("invalid POP3 configuration was accepted")
		}
	}
	if _, err := NewServer(Config{Address: "127.0.0.1:995", TLSConfig: tlsConfig}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{}, nil
	})); err != nil {
		t.Fatalf("Planner-managed POP3 listener required a deployment upstream: %v", err)
	}
}
