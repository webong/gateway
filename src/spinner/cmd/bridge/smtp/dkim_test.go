package smtp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/emersion/go-msgauth/dkim"
)

func TestDKIMVerifiesOriginalMessageAndReportsTampering(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("From: Alice <alice@sender.example>\r\nTo: Bob <bob@example.net>\r\nSubject: Original\r\n\r\nmessage body\r\n")
	var signed bytes.Buffer
	if err := dkim.Sign(&signed, bytes.NewReader(message), &dkim.SignOptions{
		Domain: "sender.example", Selector: "test", Signer: privateKey,
	}); err != nil {
		t.Fatal(err)
	}
	queries := 0
	verifier := dnsDKIMVerifier{lookupTXT: func(_ context.Context, domain string) ([]string, error) {
		queries++
		if domain != "test._domainkey.sender.example" {
			t.Fatalf("unexpected DKIM key lookup: %s", domain)
		}
		return []string{"v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(publicKey)}, nil
	}}
	evidence := verifier.Verify(context.Background(), signed.Bytes())
	if evidence.Result != "pass" || len(evidence.Signatures) != 1 || evidence.Signatures[0].Domain != "sender.example" || evidence.Signatures[0].OrganizationalDomain != "sender.example" || evidence.Signatures[0].Result != "pass" {
		t.Fatalf("expected valid DKIM evidence, got %+v", evidence)
	}
	tampered := strings.Replace(signed.String(), "message body", "changed body", 1)
	evidence = verifier.Verify(context.Background(), []byte(tampered))
	if evidence.Result != "fail" || evidence.Signatures[0].Result != "fail" || queries != 2 {
		t.Fatalf("expected DKIM failure after tampering, got %+v (%d queries)", evidence, queries)
	}
}

func TestDKIMNoSignatureAndTemporaryDNSFailure(t *testing.T) {
	message := []byte("From: Alice <alice@sender.example>\r\n\r\nbody\r\n")
	verifier := dnsDKIMVerifier{lookupTXT: func(context.Context, string) ([]string, error) {
		t.Fatal("unsigned mail must not query DNS")
		return nil, nil
	}}
	if evidence := verifier.Verify(context.Background(), message); evidence.Result != "none" || len(evidence.Signatures) != 0 {
		t.Fatalf("unexpected unsigned evidence: %+v", evidence)
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var signed bytes.Buffer
	if err := dkim.Sign(&signed, bytes.NewReader(message), &dkim.SignOptions{Domain: "sender.example", Selector: "test", Signer: privateKey}); err != nil {
		t.Fatal(err)
	}
	verifier.lookupTXT = func(context.Context, string) ([]string, error) {
		return nil, &net.DNSError{Err: "temporary failure", IsTemporary: true}
	}
	evidence := verifier.Verify(context.Background(), signed.Bytes())
	if evidence.Result != "temperror" || len(evidence.Signatures) != 1 || evidence.Signatures[0].Result != "temperror" {
		t.Fatalf("unexpected temporary DNS evidence: %+v", evidence)
	}
	verifier.lookupTXT = func(context.Context, string) ([]string, error) { return nil, errors.New("no key") }
	evidence = verifier.Verify(context.Background(), signed.Bytes())
	if evidence.Result != "permerror" {
		t.Fatalf("unexpected missing-key evidence: %+v", evidence)
	}
}

func TestDKIMCapsSignatureVerifications(t *testing.T) {
	message := strings.Repeat("DKIM-Signature: invalid\r\n", maxDKIMVerifications+1) + "From: Alice <alice@sender.example>\r\n\r\nbody\r\n"
	evidence := (dnsDKIMVerifier{}).Verify(context.Background(), []byte(message))
	if !evidence.Truncated || len(evidence.Signatures) != maxDKIMVerifications || evidence.Result != "permerror" {
		t.Fatalf("expected bounded DKIM evidence, got %+v", evidence)
	}
}
