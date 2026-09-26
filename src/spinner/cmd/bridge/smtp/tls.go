package smtp

import (
	"crypto/tls"
	"fmt"
	"strings"
)

// LoadTLSConfig loads the certificate used for SMTP STARTTLS or implicit TLS.
// Both paths must be supplied together; an empty pair deliberately disables
// TLS so callers can distinguish an explicitly configured listener from a
// plain development listener.
func LoadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	certFile = strings.TrimSpace(certFile)
	keyFile = strings.TrimSpace(keyFile)
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, fmt.Errorf("SMTP TLS certificate and key files must be configured together")
	}

	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load SMTP TLS certificate: %w", err)
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	}, nil
}
