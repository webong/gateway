package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

// Run with GATEWAY_TEST_IMAP_UPSTREAM_ADDR and GATEWAY_TEST_IMAP_CA_FILE to
// verify this gateway against a separately deployed implicit-TLS IMAP server.
func TestExternalIMAPInteroperability(t *testing.T) {
	upstreamAddr := os.Getenv("GATEWAY_TEST_IMAP_UPSTREAM_ADDR")
	caFile := os.Getenv("GATEWAY_TEST_IMAP_CA_FILE")
	if upstreamAddr == "" || caFile == "" {
		t.Skip("external IMAP upstream and certificate not configured")
	}
	upstreamPassword := os.Getenv("GATEWAY_TEST_IMAP_UPSTREAM_PASSWORD")
	if upstreamPassword == "" {
		t.Fatal("external IMAP upstream password is required")
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
		decision := bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolIMAP, Action: bridge.GatewayAccept}
		if event.Kind == bridge.EventAuthenticate {
			if event.Attributes["username"] != "alice@example.test" || event.Attributes["password"] != "client-secret" {
				return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolIMAP, Action: bridge.GatewayReject}, nil
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
	expectLine(t, reader, "* OK [CAPABILITY IMAP4rev1 AUTH=PLAIN SASL-IR] Gateway ready")
	for _, command := range []struct{ request, tag string }{
		{`A1 LOGIN "alice@example.test" "client-secret"`, "A1"},
		{"A2 CAPABILITY", "A2"},
		{`A3 LIST "" "*"`, "A3"},
	} {
		if _, err := fmt.Fprintf(client, "%s\r\n", command.request); err != nil {
			t.Fatal(err)
		}
		found := false
		for i := 0; i < 64; i++ {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("%s: read response: %v", command.tag, err)
			}
			if strings.HasPrefix(line, command.tag+" ") {
				if !strings.HasPrefix(line, command.tag+" OK") {
					t.Fatalf("%s failed: %q", command.tag, line)
				}
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s response exceeded 64 lines", command.tag)
		}
	}
	_, _ = client.Write([]byte("A4 LOGOUT\r\n"))
}
