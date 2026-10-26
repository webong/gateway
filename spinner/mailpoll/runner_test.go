package mailpoll

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

type testControl struct {
	mu        sync.Mutex
	claim     Claim
	acks      int
	baselines int
}

func (c *testControl) Claim(context.Context, string, ClaimAction) (Claim, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.claim, nil
}
func (c *testControl) Ack(_ context.Context, _ string, _ string, validity, uid uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.acks++
	c.claim.Cursor = Cursor{UIDValidity: validity, LastUID: uid}
	return nil
}
func (c *testControl) Baseline(_ context.Context, _ string, _ string, validity, uid uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baselines++
	c.claim.Cursor = Cursor{UIDValidity: validity, LastUID: uid}
	return nil
}
func (c *testControl) Release(context.Context, string, string) error { return nil }

func TestIMAPToSignedHTTPSRetriesWithoutDuplicateAcknowledgement(t *testing.T) {
	var callbackCalls int
	var eventIDs []string
	callback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		hash := sha256.Sum256(body)
		if got := r.Header.Get("X-Gateway-Signature"); got != Sign("route-1", r.Header.Get("X-Gateway-Event-Id"),
			r.Header.Get("X-Gateway-Envelope"), hex.EncodeToString(hash[:]), "secret") {
			t.Errorf("invalid callback signature: %q", got)
		}
		if !bytes.Contains(body, []byte("Subject: ")) {
			t.Errorf("MIME body missing: %q", body)
		}
		eventIDs = append(eventIDs, r.Header.Get("X-Gateway-Event-Id"))
		callbackCalls++
		if callbackCalls == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer callback.Close()

	user := imapmemserver.NewUser("support@example.test", "password")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	message := []byte("From: sender@example.test\r\nTo: support@example.test\r\nSubject: Ticket\r\n\r\nHelp")
	if _, err := user.Append("INBOX", literal{bytes.NewReader(message)}, &imap.AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	backend := imapmemserver.New()
	backend.AddUser(user)
	server := imapserver.New(&imapserver.Options{NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return backend.NewSession(), nil, nil
	}})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secure := tls.NewListener(listener, &tls.Config{Certificates: callback.TLS.Certificates, MinVersion: tls.VersionTLS12})
	go server.Serve(secure)
	defer server.Close()

	control := &testControl{claim: Claim{RouteID: "route-1", Address: "support@example.test",
		CallbackURL: callback.URL, WebhookSecret: "secret", UpstreamAddress: listener.Addr().String(),
		UpstreamServerName: "example.com", Username: "support@example.test", Secret: "password",
		Mechanism: "PLAIN", LeaseToken: "lease-1", InitialSync: InitialSyncAll}}
	roots := x509.NewCertPool()
	roots.AddCert(callback.Certificate())
	runner := Runner{Control: control, Dial: func(ctx context.Context, c Claim) (net.Conn, error) {
		connection, err := tls.DialWithDialer(&net.Dialer{}, "tcp", c.UpstreamAddress,
			&tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12})
		return connection, err
	}, Deliver: func(ctx context.Context, target string, headers http.Header, body []byte) (int, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			return 0, err
		}
		request.Header = headers
		response, err := callback.Client().Do(request)
		if err != nil {
			return 0, err
		}
		defer response.Body.Close()
		return response.StatusCode, nil
	}}
	if err := runner.Run(context.Background(), "route-1"); err == nil {
		t.Fatal("expected first callback failure")
	}
	if control.acks != 0 {
		t.Fatalf("cursor advanced on 503: %d", control.acks)
	}
	if err := runner.Run(context.Background(), "route-1"); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), "route-1"); err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 2 || control.acks != 1 || eventIDs[0] != eventIDs[1] {
		t.Fatalf("retry/dedupe mismatch: callbacks=%d acks=%d ids=%v", callbackCalls, control.acks, eventIDs)
	}
	if err := user.Delete("INBOX"); err != nil {
		t.Fatal(err)
	}
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := user.Append("INBOX", literal{bytes.NewReader(message)}, &imap.AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), "route-1"); err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 2 || control.acks != 1 || control.baselines != 1 {
		t.Fatalf("UIDVALIDITY reset imported history: callbacks=%d acks=%d baselines=%d", callbackCalls, control.acks, control.baselines)
	}
	newMessage := []byte("From: sender@example.test\r\nTo: support@example.test\r\nSubject: New\r\n\r\nNew message")
	if _, err := user.Append("INBOX", literal{bytes.NewReader(newMessage)}, &imap.AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), "route-1"); err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 3 || control.acks != 2 || eventIDs[2] == eventIDs[1] {
		t.Fatalf("post-baseline delivery mismatch: callbacks=%d acks=%d ids=%v", callbackCalls, control.acks, eventIDs)
	}
}

func TestDefaultInitialSyncBaselinesExistingMailAndDeliversOnlyLaterMail(t *testing.T) {
	callbackCalls := 0
	callback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callbackCalls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()

	user := imapmemserver.NewUser("support@example.test", "password")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	message := func(subject string) []byte {
		return []byte("From: sender@example.test\r\nTo: support@example.test\r\nSubject: " + subject + "\r\n\r\nbody")
	}
	if _, err := user.Append("INBOX", literal{bytes.NewReader(message("Backlog"))}, &imap.AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	backend := imapmemserver.New()
	backend.AddUser(user)
	server := imapserver.New(&imapserver.Options{NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return backend.NewSession(), nil, nil
	}})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	secure := tls.NewListener(listener, &tls.Config{Certificates: callback.TLS.Certificates, MinVersion: tls.VersionTLS12})
	go server.Serve(secure)
	defer server.Close()

	control := &testControl{claim: Claim{RouteID: "route-new", Address: "support@example.test",
		CallbackURL: callback.URL, WebhookSecret: "secret", UpstreamAddress: listener.Addr().String(),
		UpstreamServerName: "example.com", Username: "support@example.test", Secret: "password",
		Mechanism: "PLAIN", LeaseToken: "lease-new", InitialSync: InitialSyncNew}}
	roots := x509.NewCertPool()
	roots.AddCert(callback.Certificate())
	runner := Runner{
		Control: control,
		Dial: func(ctx context.Context, claim Claim) (net.Conn, error) {
			return tls.DialWithDialer(&net.Dialer{}, "tcp", claim.UpstreamAddress,
				&tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12})
		},
		Deliver: func(ctx context.Context, target string, headers http.Header, body []byte) (int, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
			if err != nil {
				return 0, err
			}
			request.Header = headers
			response, err := callback.Client().Do(request)
			if err != nil {
				return 0, err
			}
			defer response.Body.Close()
			return response.StatusCode, nil
		},
	}
	if err := runner.Run(context.Background(), "route-new"); err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 0 || control.baselines != 1 || control.claim.Cursor.LastUID != 1 {
		t.Fatalf("first poll did not baseline backlog: callbacks=%d baselines=%d cursor=%+v", callbackCalls, control.baselines, control.claim.Cursor)
	}
	if _, err := user.Append("INBOX", literal{bytes.NewReader(message("New"))}, &imap.AppendOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), "route-new"); err != nil {
		t.Fatal(err)
	}
	if callbackCalls != 1 || control.acks != 1 || control.claim.Cursor.LastUID != 2 {
		t.Fatalf("new message was not delivered once: callbacks=%d acks=%d cursor=%+v", callbackCalls, control.acks, control.claim.Cursor)
	}
}

type literal struct{ *bytes.Reader }

func (l literal) Size() int64 { return int64(l.Len()) }
