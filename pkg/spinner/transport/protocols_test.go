package transport

import (
	"path/filepath"
	"testing"

	relayconfig "github.com/webong/gateway/pkg/spinner/internal/config"
)

func TestNewEmailProtectionAllowsQuarantineWithoutMalwareScanner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quarantine")
	scanner, quarantine, err := NewEmailProtection(&relayconfig.Config{EmailQuarantinePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if scanner != nil || quarantine == nil {
		t.Fatalf("expected quarantine store without malware scanner, got scanner=%v quarantine=%v", scanner, quarantine)
	}
}

func TestNewSMTPAdapterRejectsInvalidPrivateDKIMResolver(t *testing.T) {
	_, err := NewSMTPAdapter(&relayconfig.Config{
		SMTPRelayAddress:     "127.0.0.1:2526",
		SMTPRelayTLSMode:     "none",
		EmailDKIMResolverURL: "http://public.example.test",
		InternalToken:        "private-token",
	})
	if err == nil {
		t.Fatal("SMTP adapter accepted a non-private DKIM resolver")
	}
}
