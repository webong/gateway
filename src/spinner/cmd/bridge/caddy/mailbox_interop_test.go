package caddy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

// scripts/mailbox-smoke.sh supplies independently running Dovecot servers.
// This exercises the Caddy app lifecycle and its private HTTP Planner hop,
// beyond the direct POP3/IMAP server interoperability tests.
func TestExternalMailboxCaddyInteroperability(t *testing.T) {
	password := os.Getenv("GATEWAY_TEST_MAILBOX_UPSTREAM_PASSWORD")
	if password == "" {
		t.Skip("external mailbox upstream password is not configured")
	}
	certFile, keyFile := mailboxTestCertificate(t)
	certificate, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		t.Fatal("gateway test certificate is invalid")
	}
	for _, kind := range []string{"pop3", "imap"} {
		t.Run(kind, func(t *testing.T) {
			upstreamAddress := os.Getenv("GATEWAY_TEST_" + strings.ToUpper(kind) + "_UPSTREAM_ADDR")
			upstreamCA := os.Getenv("GATEWAY_TEST_" + strings.ToUpper(kind) + "_CA_FILE")
			if upstreamAddress == "" || upstreamCA == "" {
				t.Skip("external " + kind + " upstream is not configured")
			}
			const internalToken = "mailbox-caddy-test-token"
			var authenticated atomic.Int32
			planner := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != "/_internal/gateway/event" || request.Header.Get(bridge.InternalTokenHeader) != internalToken {
					http.Error(writer, "unauthorized", http.StatusUnauthorized)
					return
				}
				var event bridge.GatewayEvent
				if err := json.NewDecoder(request.Body).Decode(&event); err != nil || string(event.Protocol) != kind {
					http.Error(writer, "invalid event", http.StatusBadRequest)
					return
				}
				decision := bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: event.Protocol, Action: bridge.GatewayAccept}
				if event.Kind == bridge.EventAuthenticate {
					if event.Attributes["username"] != "alice@example.test" || event.Attributes["password"] != "client-secret" {
						decision.Action = bridge.GatewayReject
					} else {
						authenticated.Add(1)
						decision.Metadata = map[string]string{
							"identity": "account-1", "upstream_id": "dovecot",
							"upstream_username": "alice@example.test", "upstream_password": password,
						}
					}
				}
				writer.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(writer).Encode(decision)
			}))
			defer planner.Close()

			options := MailboxAppOptions{
				Listen: "127.0.0.1:0", PlannerURL: planner.URL, InternalToken: internalToken,
				Hostname: "localhost", TLSCertFile: certFile, TLSKeyFile: keyFile, UpstreamCAFile: upstreamCA,
				Upstreams: map[string]MailboxUpstream{"dovecot": {Address: upstreamAddress, ServerName: "localhost"}},
			}
			var app interface {
				Provision(caddy.Context) error
				Start() error
				Stop() error
			}
			var listener func() net.Listener
			if kind == "pop3" {
				pop := &POP3App{MailboxAppOptions: options}
				app = pop
				listener = func() net.Listener { return pop.listener }
			} else {
				imapApp := &IMAPApp{MailboxAppOptions: options}
				app = imapApp
				listener = func() net.Listener { return imapApp.listener }
			}
			if err := app.Provision(caddy.Context{}); err != nil {
				t.Fatal(err)
			}
			if err := app.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = app.Stop() }()
			client, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", listener().Addr().String(), &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(15 * time.Second))
			reader := bufio.NewReader(client)
			greeting, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if kind == "pop3" {
				if !strings.HasPrefix(greeting, "+OK") {
					t.Fatalf("unexpected POP3 greeting: %q", greeting)
				}
				for _, exchange := range [][2]string{{"USER alice@example.test", "+OK"}, {"PASS client-secret", "+OK"}, {"STAT", "+OK"}} {
					if _, err := fmt.Fprintf(client, "%s\r\n", exchange[0]); err != nil {
						t.Fatal(err)
					}
					line, err := reader.ReadString('\n')
					if err != nil || !strings.HasPrefix(line, exchange[1]) {
						t.Fatalf("POP3 %s returned %q, %v", exchange[0], line, err)
					}
				}
				_, _ = fmt.Fprint(client, "QUIT\r\n")
			} else {
				if !strings.HasPrefix(greeting, "* OK") {
					t.Fatalf("unexpected IMAP greeting: %q", greeting)
				}
				for _, command := range []struct{ text, tag string }{
					{`A1 LOGIN "alice@example.test" "client-secret"`, "A1"},
					{`A2 LIST "" "*"`, "A2"},
				} {
					if _, err := fmt.Fprintf(client, "%s\r\n", command.text); err != nil {
						t.Fatal(err)
					}
					matched := false
					for i := 0; i < 64; i++ {
						line, err := reader.ReadString('\n')
						if err != nil {
							t.Fatalf("IMAP %s read: %v", command.tag, err)
						}
						if strings.HasPrefix(line, command.tag+" ") {
							if !strings.HasPrefix(line, command.tag+" OK") {
								t.Fatalf("IMAP %s returned %q", command.tag, line)
							}
							matched = true
							break
						}
					}
					if !matched {
						t.Fatalf("IMAP %s returned no tagged completion", command.tag)
					}
				}
				_, _ = fmt.Fprint(client, "A3 LOGOUT\r\n")
			}
			if authenticated.Load() != 1 {
				t.Fatalf("Planner did not approve exactly one %s mailbox authentication", kind)
			}
		})
	}
}
