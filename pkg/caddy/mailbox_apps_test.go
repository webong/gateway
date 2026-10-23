package caddy

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/webong/gateway/pkg/spinner/bridge/imap"
	"github.com/webong/gateway/pkg/spinner/bridge/pop3"
)

func TestMailboxAppsParseCaddyfileAllowedHosts(t *testing.T) {
	for _, test := range []struct {
		name  string
		parse func(*caddyfile.Dispenser, any) (any, error)
	}{
		{"gateway_pop3", parsePOP3App},
		{"gateway_imap", parseIMAPApp},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := test.parse(caddyfile.NewTestDispenser(test.name+` {
	listen 127.0.0.1:1995
	planner_url http://127.0.0.1:8081
	internal_token private-token
	hostname mail.example.test
	tls_cert_file /etc/gateway/mail.crt
	tls_key_file /etc/gateway/mail.key
	upstream_ca_file /etc/gateway/upstream-ca.crt
	max_connections 25
	auth_per_minute 12
	auth_burst 3
	idle_timeout 20m
	planner_timeout 4s
	upstream_timeout 5s
	max_body_size 2048
}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			result, ok := value.(httpcaddyfile.App)
			if !ok || result.Name != strings.Replace(test.name, "_", ".", 1) {
				t.Fatalf("unexpected Caddy app: %#v", value)
			}
			var config MailboxAppOptions
			if err := json.Unmarshal(result.Value, &config); err != nil {
				t.Fatal(err)
			}
			if config.Listen != "127.0.0.1:1995" || config.PlannerURL != "http://127.0.0.1:8081" || config.InternalToken != "private-token" || config.UpstreamCAFile != "/etc/gateway/upstream-ca.crt" || config.MaxConnections != 25 || time.Duration(config.IdleTimeout) != 20*time.Minute {
				t.Fatalf("unexpected mailbox app: %+v", config)
			}
			_, err = test.parse(caddyfile.NewTestDispenser(test.name+` {
	upstream primary mail.internal:995 mail.internal
}`), nil)
			if err == nil {
				t.Fatalf("legacy upstream directive was accepted: %v", err)
			}
		})
	}
}

func TestMailboxAppsProvisionTLSOnlyServers(t *testing.T) {
	certFile, keyFile := mailboxTestCertificate(t)
	options := MailboxAppOptions{
		PlannerURL: "http://127.0.0.1:8081", InternalToken: "private-token",
		TLSCertFile: certFile, TLSKeyFile: keyFile,
	}
	pop := &POP3App{MailboxAppOptions: options}
	if err := pop.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := pop.server.(*pop3.Server); !ok || pop.Listen != ":995" {
		t.Fatalf("POP3 Caddy app did not provision the existing TLS server: %+v", pop)
	}
	imapApp := &IMAPApp{MailboxAppOptions: options}
	if err := imapApp.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := imapApp.server.(*imap.Server); !ok || imapApp.Listen != ":993" {
		t.Fatalf("IMAP Caddy app did not provision the existing TLS server: %+v", imapApp)
	}
	missingTLS := &POP3App{MailboxAppOptions: MailboxAppOptions{PlannerURL: options.PlannerURL, InternalToken: options.InternalToken}}
	if err := missingTLS.Provision(caddy.Context{}); err == nil {
		t.Fatal("POP3 app accepted configuration without TLS")
	}
	invalidCA := &POP3App{MailboxAppOptions: options}
	invalidCA.UpstreamCAFile = keyFile
	if err := invalidCA.Provision(caddy.Context{}); err == nil {
		t.Fatal("POP3 app accepted an upstream CA file without certificates")
	}
}

type recordingMailboxServer struct {
	address  chan string
	stopped  chan struct{}
	listener net.Listener
}

func (server *recordingMailboxServer) Serve(listener net.Listener) error {
	server.listener = listener
	server.address <- listener.Addr().String()
	<-server.stopped
	return nil
}

func (server *recordingMailboxServer) Shutdown(context.Context) error {
	_ = server.listener.Close()
	close(server.stopped)
	return nil
}

func TestMailboxCaddyAppStartsAndStopsPrivateListener(t *testing.T) {
	server := &recordingMailboxServer{address: make(chan string, 1), stopped: make(chan struct{})}
	app := &POP3App{MailboxAppOptions: MailboxAppOptions{Listen: "127.0.0.1:0", server: server}}
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case address := <-server.address:
		if !strings.HasPrefix(address, "127.0.0.1:") {
			t.Fatalf("unexpected listener address: %s", address)
		}
	case <-time.After(time.Second):
		t.Fatal("POP3 app did not start its listener")
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestMailboxCaddyAppsServeImplicitTLSGreetings(t *testing.T) {
	certFile, keyFile := mailboxTestCertificate(t)
	certificate, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		t.Fatal("test TLS certificate could not be trusted")
	}
	for _, kind := range []string{"pop3", "imap"} {
		t.Run(kind, func(t *testing.T) {
			options := MailboxAppOptions{
				Listen: "127.0.0.1:0", PlannerURL: "http://127.0.0.1:8081", InternalToken: "private-token",
				TLSCertFile: certFile, TLSKeyFile: keyFile,
			}
			var app interface {
				Provision(caddy.Context) error
				Start() error
				Stop() error
			}
			var address func() string
			if kind == "pop3" {
				pop := &POP3App{MailboxAppOptions: options}
				app = pop
				address = func() string { return pop.listener.Addr().String() }
			} else {
				imapApp := &IMAPApp{MailboxAppOptions: options}
				app = imapApp
				address = func() string { return imapApp.listener.Addr().String() }
			}
			if err := app.Provision(caddy.Context{}); err != nil {
				t.Fatal(err)
			}
			if err := app.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = app.Stop() }()
			client, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", address(), &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(5 * time.Second))
			greeting, err := bufio.NewReader(client).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			prefix := "+OK"
			if kind == "imap" {
				prefix = "* OK"
			}
			if !strings.HasPrefix(greeting, prefix) {
				t.Fatalf("unexpected %s greeting: %q", kind, greeting)
			}
		})
	}
}

func mailboxTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature,
	}, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature,
	}, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(t.TempDir(), "mail.crt")
	keyFile := filepath.Join(filepath.Dir(certFile), "mail.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKey}), 0600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile
}
