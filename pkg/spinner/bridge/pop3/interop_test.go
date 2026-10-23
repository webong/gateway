package pop3

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

// Run with GATEWAY_TEST_POP3_UPSTREAM_ADDR and GATEWAY_TEST_POP3_CA_FILE to
// verify this gateway against a separately deployed implicit-TLS POP3 server.
func TestExternalPOP3Interoperability(t *testing.T) {
	upstreamAddr := os.Getenv("GATEWAY_TEST_POP3_UPSTREAM_ADDR")
	caFile := os.Getenv("GATEWAY_TEST_POP3_CA_FILE")
	if upstreamAddr == "" || caFile == "" {
		t.Skip("external POP3 upstream and certificate not configured")
	}
	upstreamPassword := os.Getenv("GATEWAY_TEST_POP3_UPSTREAM_PASSWORD")
	if upstreamPassword == "" {
		t.Fatal("external POP3 upstream password is required")
	}
	certificate, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	upstreamRoots := x509.NewCertPool()
	if !upstreamRoots.AppendCertsFromPEM(certificate) {
		t.Fatal("invalid upstream CA certificate")
	}
	tlsConfig, clientRoots := testTLS(t)
	planner := plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		decision := bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolPOP3, Action: bridge.GatewayAccept}
		if event.Kind == bridge.EventAuthenticate {
			if event.Attributes["username"] != "alice@example.test" || event.Attributes["password"] != "client-secret" {
				return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolPOP3, Action: bridge.GatewayReject}, nil
			}
			decision.Metadata = map[string]string{"identity": "account-1", "upstream_id": "dovecot",
				"upstream_username": "alice@example.test", "upstream_password": upstreamPassword}
		}
		return decision, nil
	})
	server, err := NewServer(Config{Address: "127.0.0.1:0", Hostname: "localhost", TLSConfig: tlsConfig,
		Upstreams: map[string]Upstream{"dovecot": {Address: upstreamAddr, ServerName: "localhost"}}, UpstreamRootCAs: upstreamRoots}, planner)
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
	client, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{RootCAs: clientRoots, ServerName: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(client)
	expectLine(t, reader, "+OK Gateway POP3 ready")
	for _, exchange := range [][2]string{{"USER alice@example.test", "+OK username received"}, {"PASS client-secret", "+OK mailbox ready"}} {
		if _, err := client.Write([]byte(exchange[0] + "\r\n")); err != nil {
			t.Fatal(err)
		}
		expectLine(t, reader, exchange[1])
	}
	_, _ = client.Write([]byte("STAT\r\n"))
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "+OK ") {
		t.Fatalf("STAT response = %q, err = %v", line, err)
	}
	_, _ = client.Write([]byte("QUIT\r\n"))
	line, err = reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "+OK") {
		t.Fatalf("QUIT response = %q, err = %v", line, err)
	}
}
