package smtpout

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/dkim"
)

func TestDKIMSignerSignsOnlyConfiguredAuthorDomain(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "dkim.key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "dkim.json")
	if err := os.WriteFile(configPath, []byte(`[{"domain":"sender.example","selector":"test","key_file":"`+keyPath+`"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := LoadDKIMSigner(configPath)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("From: Alice <alice@sender.example>\r\nTo: Bob <bob@example.net>\r\nSubject: Original\r\n\r\nmessage body\r\n")
	signed, err := signer.Sign("sender.example", message)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(message, []byte("From: Alice <alice@sender.example>\r\nTo: Bob <bob@example.net>\r\nSubject: Original\r\n\r\nmessage body\r\n")) {
		t.Fatal("signing mutated the input message")
	}
	verified, err := dkim.VerifyWithOptions(bytes.NewReader(signed), &dkim.VerifyOptions{LookupTXT: func(domain string) ([]string, error) {
		if domain != "test._domainkey.sender.example" {
			t.Fatalf("unexpected DKIM lookup: %s", domain)
		}
		return []string{"v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(publicKey)}, nil
	}})
	if err != nil || len(verified) != 1 || verified[0].Err != nil || verified[0].Domain != "sender.example" {
		t.Fatalf("expected a verifiable DKIM signature, got %+v, %v", verified, err)
	}
	if _, err := signer.Sign("other.example", message); err == nil {
		t.Fatal("unconfigured signing domain was accepted")
	}
	if _, err := signer.Sign("sender.example", []byte(strings.Replace(string(message), "sender.example", "other.example", 1))); err == nil {
		t.Fatal("author/signing domain mismatch was accepted")
	}
}

func TestDKIMSignerRejectsExposedPrivateKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "dkim.key")
	if err := os.WriteFile(keyPath, []byte("not-a-key"), 0644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "dkim.json")
	if err := os.WriteFile(configPath, []byte(`[{"domain":"sender.example","selector":"test","key_file":"`+keyPath+`"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDKIMSigner(configPath); err == nil {
		t.Fatal("world-readable DKIM private key was accepted")
	}
}
