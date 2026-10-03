package imap

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
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
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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

func TestIMAPTLSProxyAuthenticatesAndPassesMailboxCommands(t *testing.T) {
	for _, method := range []string{"LOGIN", "LOGIN literal username", "LOGIN two literals", "AUTHENTICATE PLAIN", "AUTHENTICATE PLAIN continuation"} {
		t.Run(method, func(t *testing.T) {
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
				_, _ = conn.Write([]byte("* OK external IMAP mailbox\r\n"))
				if line, _ := reader.ReadString('\n'); line != "GATEWAY1 LOGIN \"upstream-alice\" \"upstream-secret\"\r\n" {
					t.Errorf("upstream login = %q", line)
					return
				}
				_, _ = conn.Write([]byte("GATEWAY1 OK LOGIN completed\r\n"))
				if line, _ := reader.ReadString('\n'); line != "A2 SELECT INBOX\r\n" {
					t.Errorf("upstream command = %q", line)
					return
				}
				_, _ = conn.Write([]byte("* 1 EXISTS\r\nA2 OK SELECT completed\r\n"))
				if line, _ := reader.ReadString('\n'); line != "A3 LOGOUT\r\n" {
					t.Errorf("upstream logout = %q", line)
					return
				}
				_, _ = conn.Write([]byte("* BYE external IMAP closing\r\nA3 OK LOGOUT completed\r\n"))
			}()
			var mu sync.Mutex
			var kinds []bridge.EventKind
			planner := plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
				mu.Lock()
				kinds = append(kinds, event.Kind)
				mu.Unlock()
				decision := bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolIMAP, Action: bridge.GatewayAccept}
				if event.Kind == bridge.EventAuthenticate {
					if event.Attributes["username"] != "alice@example.test" || event.Attributes["password"] != "client-secret" {
						t.Error("unexpected client credentials")
					}
					decision.Metadata = map[string]string{"identity": "account-1", "upstream_id": "primary", "upstream_username": "upstream-alice", "upstream_password": "upstream-secret"}
				}
				return decision, nil
			})
			server, err := NewServer(Config{Address: "127.0.0.1:0", Hostname: "localhost", TLSConfig: tlsConfig,
				Upstreams: map[string]Upstream{"primary": {Address: upstreamListener.Addr().String(), ServerName: "localhost"}}, UpstreamRootCAs: roots}, planner)
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
			expectLine(t, reader, "* OK [CAPABILITY IMAP4rev1 AUTH=PLAIN SASL-IR] Gateway ready")
			if method == "LOGIN" {
				_, _ = client.Write([]byte("A1 LOGIN \"alice@example.test\" \"client-secret\"\r\n"))
			} else if method == "LOGIN literal username" {
				_, _ = client.Write([]byte("A1 LOGIN {18}\r\n"))
				expectLine(t, reader, "+ Ready for login literal")
				_, _ = client.Write([]byte("alice@example.test \"client-secret\"\r\n"))
			} else if method == "LOGIN two literals" {
				_, _ = client.Write([]byte("A1 LOGIN {18}\r\n"))
				expectLine(t, reader, "+ Ready for login literal")
				_, _ = client.Write([]byte("alice@example.test {13}\r\n"))
				expectLine(t, reader, "+ Ready for login literal")
				_, _ = client.Write([]byte("client-secret\r\n"))
			} else if method == "AUTHENTICATE PLAIN" {
				response := base64.StdEncoding.EncodeToString([]byte("\x00alice@example.test\x00client-secret"))
				_, _ = client.Write([]byte("A1 AUTHENTICATE PLAIN " + response + "\r\n"))
			} else {
				_, _ = client.Write([]byte("A1 AUTHENTICATE PLAIN\r\n"))
				expectLine(t, reader, "+ ")
				response := base64.StdEncoding.EncodeToString([]byte("\x00alice@example.test\x00client-secret"))
				_, _ = client.Write([]byte(response + "\r\n"))
			}
			if method == "LOGIN" || method == "LOGIN literal username" || method == "LOGIN two literals" {
				expectLine(t, reader, "A1 OK LOGIN completed")
			} else {
				expectLine(t, reader, "A1 OK AUTHENTICATE completed")
			}
			_, _ = client.Write([]byte("A2 SELECT INBOX\r\n"))
			expectLine(t, reader, "* 1 EXISTS")
			expectLine(t, reader, "A2 OK SELECT completed")
			_, _ = client.Write([]byte("A3 LOGOUT\r\n"))
			expectLine(t, reader, "* BYE external IMAP closing")
			expectLine(t, reader, "A3 OK LOGOUT completed")
			_ = client.Close()
			select {
			case <-upstreamDone:
			case <-time.After(5 * time.Second):
				t.Fatal("upstream did not finish")
			}
			deadline := time.Now().Add(time.Second)
			for {
				mu.Lock()
				got := append([]bridge.EventKind(nil), kinds...)
				mu.Unlock()
				if len(got) == 3 {
					if got[0] != bridge.EventAuthenticate || got[1] != bridge.EventConnect || got[2] != bridge.EventClose {
						t.Fatalf("events = %v", got)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("events = %v", got)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

func TestIMAPLoginParserRejectsUnsafeOrUnsupportedSyntax(t *testing.T) {
	for _, test := range []struct {
		input, username, password string
		ok                        bool
	}{
		{`"alice@example.test" "a b"`, "alice@example.test", "a b", true},
		{`alice secret`, "alice", "secret", true},
		{`"ali\\\"ce" "secret"`, `ali\"ce`, "secret", true},
		{`{5} secret`, "", "", false},
		{`alice secret extra`, "", "", false},
		{`alice "unterminated`, "", "", false},
	} {
		username, password, ok := parseLogin(test.input)
		if username != test.username || password != test.password || ok != test.ok {
			t.Errorf("parseLogin(%q) = %q, %q, %t", test.input, username, password, ok)
		}
	}
}

func TestIMAPRejectsUnregisteredUpstreamAndLimitsAcrossConnections(t *testing.T) {
	tlsConfig, roots := testTLS(t)
	var plannerCalls atomic.Int32
	planner := plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		plannerCalls.Add(1)
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolIMAP, Action: bridge.GatewayAccept,
			Metadata: map[string]string{"identity": "account-1", "upstream_id": "unregistered", "upstream_username": "alice", "upstream_password": "secret"}}, nil
	})
	server, err := NewServer(Config{Address: "127.0.0.1:0", TLSConfig: tlsConfig, AuthPerMinute: 1, AuthBurst: 1,
		Upstreams: map[string]Upstream{"primary": {Address: "127.0.0.1:993", ServerName: "localhost"}}}, planner)
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
	for _, expected := range []string{"A1 NO mailbox service unavailable", "A1 NO authentication rate limit exceeded"} {
		client, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "localhost"})
		if err != nil {
			t.Fatal(err)
		}
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(client)
		expectLine(t, reader, "* OK [CAPABILITY IMAP4rev1 AUTH=PLAIN SASL-IR] Gateway ready")
		_, _ = client.Write([]byte("A1 LOGIN alice secret\r\n"))
		expectLine(t, reader, expected)
		_ = client.Close()
	}
	if plannerCalls.Load() != 1 {
		t.Fatalf("Planner calls = %d, want 1", plannerCalls.Load())
	}
}

func TestIMAPRejectsOversizedAndUnadvertisedLoginLiteralsBeforePlanner(t *testing.T) {
	tlsConfig, roots := testTLS(t)
	var plannerCalls atomic.Int32
	planner := plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		plannerCalls.Add(1)
		return bridge.GatewayDecision{}, nil
	})
	server, err := NewServer(Config{Address: "127.0.0.1:0", TLSConfig: tlsConfig,
		Upstreams: map[string]Upstream{"primary": {Address: "127.0.0.1:993", ServerName: "localhost"}}}, planner)
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
	for _, command := range []string{"A1 LOGIN {2049}\r\n", "A1 LOGIN {18+}\r\n"} {
		client, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "localhost"})
		if err != nil {
			t.Fatal(err)
		}
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(client)
		expectLine(t, reader, "* OK [CAPABILITY IMAP4rev1 AUTH=PLAIN SASL-IR] Gateway ready")
		_, _ = client.Write([]byte(command))
		expectLine(t, reader, "A1 BAD invalid or oversized login literal")
		_ = client.Close()
	}
	if plannerCalls.Load() != 0 {
		t.Fatalf("invalid literals called Planner %d times", plannerCalls.Load())
	}
}

func TestIMAPAuthLimiterRefillsAndBoundsSources(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := newAuthLimiter(2, 2)
	limiter.now = func() time.Time { return now }
	if !limiter.allow("192.0.2.1:1000") || !limiter.allow("[::ffff:192.0.2.1]:1001") || limiter.allow("192.0.2.1:1002") {
		t.Fatal("one normalized source exceeded burst")
	}
	now = now.Add(30 * time.Second)
	if !limiter.allow("192.0.2.1:1003") || limiter.allow("192.0.2.1:1004") {
		t.Fatal("refill was incorrect")
	}
	limiter.max = 1
	if limiter.allow("192.0.2.2:1000") {
		t.Fatal("tracked-source bound was exceeded")
	}
	now = now.Add(authSourceIdleTTL)
	if !limiter.allow("192.0.2.2:1000") || len(limiter.entries) != 1 {
		t.Fatal("idle source was not replaced")
	}
}
