package smtp

import "testing"

func TestAuthorAttributesUseOneValidFromAndPublicSuffix(t *testing.T) {
	attributes := authorAttributes([]byte("From: Alice <alice@alerts.example.co.uk>\r\nAuthentication-Results: gateway; dmarc=pass\r\n\r\nbody\r\n"))
	if attributes["rfc5322_from_domain"] != "alerts.example.co.uk" || attributes["rfc5322_from_organizational_domain"] != "example.co.uk" {
		t.Fatalf("unexpected author identity: %+v", attributes)
	}
	if organization := organizationalDomain("mailer.example.co.uk"); organization != "example.co.uk" {
		t.Fatalf("unexpected organizational domain: %s", organization)
	}
}

func TestAuthorAttributesExcludeAmbiguousFrom(t *testing.T) {
	for _, message := range []string{
		"Subject: no From\r\n\r\nbody\r\n",
		"From: alice@example.test\r\nFrom: attacker@other.test\r\n\r\nbody\r\n",
		"From: alice@example.test, bob@other.test\r\n\r\nbody\r\n",
		"From: not-an-address\r\n\r\nbody\r\n",
	} {
		if attributes := authorAttributes([]byte(message)); len(attributes) != 0 {
			t.Fatalf("ambiguous author produced DMARC identity: %+v", attributes)
		}
	}
}
