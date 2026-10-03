package smtp

import (
	"bytes"
	"net/mail"
	"strings"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

// authorDomain reads the original RFC 5322 From field. Multiple or malformed
// author identities are not usable as a DMARC policy domain.
func authorDomain(message []byte) string {
	address := authorAddress(message)
	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return ""
	}
	return normalizedMailDomain(address[at+1:])
}

func authorAddress(message []byte) string {
	parsed, err := mail.ReadMessage(bytes.NewReader(message))
	if err != nil || len(parsed.Header["From"]) != 1 {
		return ""
	}
	addresses, err := mail.ParseAddressList(parsed.Header.Get("From"))
	if err != nil || len(addresses) != 1 {
		return ""
	}
	return addresses[0].Address
}

func normalizedMailDomain(domain string) string {
	domain = strings.TrimSuffix(strings.TrimSpace(domain), ".")
	ascii, err := idna.Lookup.ToASCII(domain)
	if err != nil || len(ascii) == 0 || len(ascii) > 253 || !strings.Contains(ascii, ".") {
		return ""
	}
	return strings.ToLower(ascii)
}

func organizationalDomain(domain string) string {
	normalized := normalizedMailDomain(domain)
	if normalized == "" {
		return ""
	}
	organization, err := publicsuffix.EffectiveTLDPlusOne(normalized)
	if err != nil {
		return ""
	}
	return organization
}

func authorAttributes(message []byte) map[string]string {
	attributes := make(map[string]string, 2)
	fromDomain := authorDomain(message)
	if fromDomain != "" {
		attributes["rfc5322_from_domain"] = fromDomain
		if organization := organizationalDomain(fromDomain); organization != "" {
			attributes["rfc5322_from_organizational_domain"] = organization
		}
	}
	return attributes
}
