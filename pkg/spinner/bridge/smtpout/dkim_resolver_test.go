package smtpout

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPDKIMSignerResolvesCurrentKeyAndRevocation(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	enabled := true
	queries := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		queries++
		if request.Header.Get("X-Gateway-Internal") != "private-token" || request.URL.Path != "/_internal/gateway/email/dkim/example.test" {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		if !enabled {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if request.Method == http.MethodHead {
			writer.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"domain": "example.test", "selector": "mail1", "private_key_pem": privatePEM})
	}))
	defer server.Close()
	signer, err := NewHTTPDKIMSigner(server.URL, "private-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.CheckSigningDomain("example.test"); err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign("example.test", []byte("From: sender@example.test\r\nSubject: test\r\n\r\nbody\r\n"))
	if err != nil || !strings.Contains(string(signed), "DKIM-Signature:") {
		t.Fatalf("message was not signed: %v", err)
	}
	if queries != 2 {
		t.Fatalf("key was cached across attempts: %d", queries)
	}
	enabled = false
	if err := signer.CheckSigningDomain("example.test"); err == nil {
		t.Fatal("revoked signing key was accepted")
	}
}
