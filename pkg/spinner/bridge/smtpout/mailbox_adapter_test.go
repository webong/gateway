package smtpout

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	smtpserver "github.com/emersion/go-smtp"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
)

func TestMailboxResolverFetchesEachConnectionsCurrentCredential(t *testing.T) {
	first := "11111111-1111-4111-8111-111111111111"
	second := "22222222-2222-4222-8222-222222222222"
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Gateway-Internal") != "private-token" {
			t.Error("missing internal credential")
			writer.WriteHeader(401)
			return
		}
		id := strings.TrimPrefix(request.URL.Path, "/_internal/gateway/mailbox-connections/")
		id = strings.TrimSuffix(id, "/smtp")
		requests[id]++
		_ = json.NewEncoder(writer).Encode(MailboxCredential{UpstreamID: "smtp-a", Username: id, Secret: "current-secret", Mechanism: "PLAIN", Address: id + "@example.test"})
	}))
	defer server.Close()
	resolver, err := NewHTTPMailboxResolver(server.URL, "private-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first, second, first} {
		credential, err := resolver.Resolve(context.Background(), id)
		if err != nil || credential.Username != id {
			t.Fatalf("resolve %s: %+v, %v", id, credential, err)
		}
	}
	if requests[first] != 2 || requests[second] != 1 {
		t.Fatalf("credentials were cached across attempts: %+v", requests)
	}
}

type staticMailboxResolver struct{ credential MailboxCredential }

func (r staticMailboxResolver) Resolve(context.Context, string) (MailboxCredential, error) {
	return r.credential, nil
}

type unavailableMailboxResolver struct{}

func (unavailableMailboxResolver) Resolve(context.Context, string) (MailboxCredential, error) {
	return MailboxCredential{}, ErrMailboxConnectionUnavailable
}

func TestManagedMailboxAdapterStopsRetryingRevokedConnection(t *testing.T) {
	adapter, err := NewManagedMailboxAdapter(unavailableMailboxResolver{}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.DeliverGateway(context.Background(), bridge.GatewayDelivery{
		Protocol: bridge.ProtocolSMTP, Adapter: "mailbox.smtp", Target: "recipient@example.test",
		Attributes: map[string]string{"mailbox_connection_id": "11111111-1111-4111-8111-111111111111", "mail_from": "a@example.test"},
		Payload:    []byte("Subject: test\r\n\r\nbody\r\n"),
	})
	if !errors.Is(err, bridge.ErrPermanentDelivery) {
		t.Fatalf("revoked mailbox remained retryable: %v", err)
	}
}

func TestManagedMailboxAdapterEnforcesEndpointShapeAndTLS(t *testing.T) {
	credential := MailboxCredential{UpstreamID: "smtp-a", Username: "account-a", Secret: "secret",
		Mechanism: "PLAIN", Address: "a@example.test", UpstreamAddress: "blocked.test:465",
		UpstreamServerName: "blocked.test", UpstreamTLSMode: "implicit", UpstreamNetworkScope: "invalid"}
	adapter, err := NewManagedMailboxAdapter(staticMailboxResolver{credential}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	delivery := bridge.GatewayDelivery{Protocol: bridge.ProtocolSMTP, Adapter: "mailbox.smtp",
		Target: "recipient@example.test", Attributes: map[string]string{
			"mailbox_connection_id": "11111111-1111-4111-8111-111111111111", "mail_from": "a@example.test"},
		Payload: []byte("Subject: test\r\n\r\nbody\r\n")}
	if err := adapter.DeliverGateway(context.Background(), delivery); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid network scope was accepted: %v", err)
	}
	credential.UpstreamAddress = "smtp.example.test:465"
	credential.UpstreamServerName = "smtp.example.test"
	credential.UpstreamNetworkScope = "public"
	credential.UpstreamTLSMode = "none"
	adapter, err = NewManagedMailboxAdapter(staticMailboxResolver{credential}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.DeliverGateway(context.Background(), delivery); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("non-TLS upstream was accepted: %v", err)
	}
}

func TestMailboxAdapterRejectsCrossAccountSenderBeforeSMTP(t *testing.T) {
	adapter, err := NewMailboxAdapter(staticMailboxResolver{MailboxCredential{
		UpstreamID: "smtp-a", Username: "account-a", Secret: "secret", Mechanism: "PLAIN", Address: "a@example.test",
	}}, map[string]Config{"smtp-a": {Address: "smtp.example.test:465", ServerName: "smtp.example.test", TLSMode: TLSImplicit}})
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.DeliverGateway(context.Background(), bridge.GatewayDelivery{
		Protocol: bridge.ProtocolSMTP, Adapter: "mailbox.smtp", Target: "recipient@example.test",
		Attributes: map[string]string{"mailbox_connection_id": "11111111-1111-4111-8111-111111111111", "mail_from": "b@example.test"},
		Payload:    []byte("Subject: test\r\n\r\nbody\r\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("cross-account sender was accepted: %v", err)
	}
}

type credentialSMTPBackend struct {
	username string
	password string
	message  []byte
}

func (backend *credentialSMTPBackend) NewSession(*smtpserver.Conn) (smtpserver.Session, error) {
	return &credentialSMTPSession{backend: backend}, nil
}

type credentialSMTPSession struct{ backend *credentialSMTPBackend }

func (*credentialSMTPSession) AuthMechanisms() []string { return []string{"PLAIN"} }
func (session *credentialSMTPSession) Auth(mechanism string) (sasl.Server, error) {
	if mechanism != "PLAIN" {
		return nil, smtpserver.ErrAuthUnsupported
	}
	return sasl.NewPlainServer(func(_, username, password string) error {
		if username != session.backend.username || password != session.backend.password {
			return errors.New("bad credentials")
		}
		return nil
	}), nil
}
func (*credentialSMTPSession) Mail(string, *smtpserver.MailOptions) error { return nil }
func (*credentialSMTPSession) Rcpt(string, *smtpserver.RcptOptions) error { return nil }
func (session *credentialSMTPSession) Data(reader io.Reader) error {
	message, err := io.ReadAll(reader)
	if err == nil {
		session.backend.message = message
	}
	return err
}
func (*credentialSMTPSession) Reset()        {}
func (*credentialSMTPSession) Logout() error { return nil }

type mapMailboxResolver map[string]MailboxCredential

func (resolver mapMailboxResolver) Resolve(_ context.Context, id string) (MailboxCredential, error) {
	credential, ok := resolver[id]
	if !ok {
		return MailboxCredential{}, errors.New("unknown connection")
	}
	return credential, nil
}

func TestMailboxAdapterRoutesTwoAccountsToSeparateApprovedSMTPServers(t *testing.T) {
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
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	serverTLS := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}, MinVersion: tls.VersionTLS12}
	clientTLS := &tls.Config{RootCAs: roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}
	backends := map[string]*credentialSMTPBackend{
		"smtp-a": {username: "alice", password: "alice-secret"},
		"smtp-b": {username: "bob", password: "bob-secret"},
	}
	upstreams := map[string]Config{}
	for id, backend := range backends {
		server := smtpserver.NewServer(backend)
		server.TLSConfig = serverTLS
		listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
		if err != nil {
			t.Fatal(err)
		}
		serveErr := make(chan error, 1)
		go func() { serveErr <- server.Serve(listener) }()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
			<-serveErr
		})
		upstreams[id] = Config{Address: listener.Addr().String(), ServerName: "localhost", TLSMode: TLSImplicit, TLSConfig: clientTLS, Timeout: time.Second}
	}
	aliceID := "11111111-1111-4111-8111-111111111111"
	bobID := "22222222-2222-4222-8222-222222222222"
	adapter, err := NewMailboxAdapter(mapMailboxResolver{
		aliceID: {UpstreamID: "smtp-a", Username: "alice", Secret: "alice-secret", Mechanism: "PLAIN", Address: "alice@example.test"},
		bobID:   {UpstreamID: "smtp-b", Username: "bob", Secret: "bob-secret", Mechanism: "PLAIN", Address: "bob@example.test"},
	}, upstreams)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ id, address, subject string }{{aliceID, "alice@example.test", "Alice"}, {bobID, "bob@example.test", "Bob"}} {
		err := adapter.DeliverGateway(context.Background(), bridge.GatewayDelivery{
			Protocol: bridge.ProtocolSMTP, Adapter: "mailbox.smtp", Target: "recipient@example.test",
			Attributes: map[string]string{"mailbox_connection_id": test.id, "mail_from": test.address},
			Payload:    []byte("Subject: " + test.subject + "\r\n\r\nbody\r\n"),
		})
		if err != nil {
			t.Fatalf("deliver %s: %v", test.address, err)
		}
	}
	if !strings.Contains(string(backends["smtp-a"].message), "Subject: Alice") || !strings.Contains(string(backends["smtp-b"].message), "Subject: Bob") {
		t.Fatalf("message crossed SMTP accounts: a=%q b=%q", backends["smtp-a"].message, backends["smtp-b"].message)
	}
}
