package smtp

import (
	"bytes"
	"testing"
)

func TestPrepareAuthenticatedSubmission(t *testing.T) {
	message := []byte("From: Alice <alice@example.test>\r\nSubject: hello\r\n\r\nbody\r\n")
	marked, domain, err := PrepareAuthenticatedSubmission("alice@example.test", []string{"alice@example.test"}, []string{"bob@example.test"}, message, []byte("private-loop-key"))
	if err != nil || domain != "example.test" || !bytes.Contains(marked, []byte("X-Gateway-Forwarded: v1.")) || !bytes.HasSuffix(marked, message) {
		t.Fatalf("submission was not prepared: domain=%q err=%v", domain, err)
	}
	for _, test := range []struct {
		name, mailFrom, raw string
		senders, recipients []string
	}{
		{"unauthorized envelope", "other@example.test", string(message), []string{"alice@example.test"}, []string{"bob@example.test"}},
		{"unauthorized author", "bob@example.test", string(message), []string{"bob@example.test"}, []string{"bob@example.test"}},
		{"spoofed grant", "alice@example.test", string(message), []string{"Alice <alice@example.test>"}, []string{"bob@example.test"}},
		{"invalid recipient", "alice@example.test", string(message), []string{"alice@example.test"}, []string{"Bob <bob@example.test>"}},
		{"ambiguous author", "alice@example.test", "From: alice@example.test\r\nFrom: other@example.test\r\n\r\nbody", []string{"alice@example.test"}, []string{"bob@example.test"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := PrepareAuthenticatedSubmission(test.mailFrom, test.senders, test.recipients, []byte(test.raw), []byte("private-loop-key")); err == nil {
				t.Fatal("accepted invalid authenticated submission")
			}
		})
	}
}
