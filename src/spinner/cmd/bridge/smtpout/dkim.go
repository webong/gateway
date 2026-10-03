package smtpout

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"os"
	"strings"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/miekg/dns"
	"golang.org/x/net/idna"
)

type dkimKeySpec struct {
	Domain   string `json:"domain"`
	Selector string `json:"selector"`
	KeyFile  string `json:"key_file"`
}

type dkimSigningKey struct {
	selector string
	signer   crypto.Signer
}

// DKIMSigner owns deployment-provided keys. A key is selected only for an
// author domain explicitly attached by authenticated SMTP ingress.
type DKIMSigner struct {
	keys map[string]dkimSigningKey
}

func (s *DKIMSigner) CheckSigningDomain(domain string) error {
	if s == nil {
		return nil
	}
	if _, ok := s.keys[domain]; !ok {
		return fmt.Errorf("no DKIM signing key is configured for %s", domain)
	}
	return nil
}

func LoadDKIMSigner(configPath string) (*DKIMSigner, error) {
	if strings.TrimSpace(configPath) == "" {
		return nil, nil
	}
	info, err := os.Stat(configPath)
	if err != nil {
		return nil, fmt.Errorf("stat DKIM signing configuration: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return nil, errors.New("DKIM signing configuration must be a regular file not writable by group or others")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read DKIM signing configuration: %w", err)
	}
	var specs []dkimKeySpec
	if err := json.Unmarshal(data, &specs); err != nil || len(specs) == 0 || len(specs) > 100 {
		return nil, errors.New("DKIM signing configuration must be a non-empty array of at most 100 keys")
	}
	signer := &DKIMSigner{keys: make(map[string]dkimSigningKey, len(specs))}
	for _, spec := range specs {
		domain := normalizedSigningDomain(spec.Domain)
		if domain == "" || domain != spec.Domain || !validSelector(spec.Selector) || spec.KeyFile == "" {
			return nil, errors.New("DKIM signing key has an invalid domain, selector, or key file")
		}
		if _, duplicate := signer.keys[domain]; duplicate {
			return nil, fmt.Errorf("DKIM signing domain %q is configured more than once", domain)
		}
		key, err := loadSigningKey(spec.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load DKIM key for %s: %w", domain, err)
		}
		signer.keys[domain] = dkimSigningKey{selector: spec.Selector, signer: key}
	}
	return signer, nil
}

func loadSigningKey(path string) (crypto.Signer, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("DKIM private key must be a regular file readable only by its owner")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("DKIM private key must contain exactly one PEM block")
	}
	var key any
	switch block.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		return nil, errors.New("DKIM private key must be PKCS#8 or RSA PEM")
	}
	if err != nil {
		return nil, err
	}
	switch typed := key.(type) {
	case *rsa.PrivateKey:
		if typed.N.BitLen() < 2048 {
			return nil, errors.New("DKIM RSA key must be at least 2048 bits")
		}
		return typed, nil
	case ed25519.PrivateKey:
		return typed, nil
	default:
		return nil, errors.New("DKIM key must use RSA or Ed25519")
	}
}

func (s *DKIMSigner) Sign(domain string, data []byte) ([]byte, error) {
	if s == nil {
		return nil, errors.New("DKIM signer is not configured")
	}
	if err := s.CheckSigningDomain(domain); err != nil {
		return nil, err
	}
	key := s.keys[domain]
	if messageAuthorDomain(data) != domain {
		return nil, errors.New("DKIM signing domain does not match the message author")
	}
	var signed bytes.Buffer
	if err := dkim.Sign(&signed, bytes.NewReader(data), &dkim.SignOptions{
		Domain: domain, Selector: key.selector, Signer: key.signer,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed,
		BodyCanonicalization:   dkim.CanonicalizationRelaxed,
	}); err != nil {
		return nil, fmt.Errorf("sign outbound message: %w", err)
	}
	return signed.Bytes(), nil
}

func messageAuthorDomain(data []byte) string {
	message, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil || len(message.Header["From"]) != 1 {
		return ""
	}
	addresses, err := mail.ParseAddressList(message.Header.Get("From"))
	if err != nil || len(addresses) != 1 {
		return ""
	}
	at := strings.LastIndexByte(addresses[0].Address, '@')
	if at < 0 {
		return ""
	}
	return normalizedSigningDomain(addresses[0].Address[at+1:])
}

func normalizedSigningDomain(value string) string {
	value = strings.TrimSuffix(strings.TrimSpace(value), ".")
	ascii, err := idna.Lookup.ToASCII(value)
	if err != nil || len(ascii) == 0 || len(ascii) > 253 || !strings.Contains(ascii, ".") || net.ParseIP(ascii) != nil {
		return ""
	}
	if _, valid := dns.IsDomainName(ascii); !valid {
		return ""
	}
	return strings.ToLower(ascii)
}

func validSelector(value string) bool {
	if len(value) == 0 || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' {
			continue
		}
		return false
	}
	return true
}
