package smtp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	forwardingHeader  = "X-Gateway-Forwarded"
	maxForwardingHops = 3
	maxReceivedHops   = 100
	markerContext     = "gateway-email-forward-v1\n"
)

// The marker contains no recipient address. Only this deployment's signed
// values count, so an arbitrary incoming header cannot stop forwarding.
func markForwarding(message, key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("forwarding-loop key is required")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("create forwarding marker: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(nonce)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(markerContext + encoded))
	value := "v1." + encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	prefix := []byte(forwardingHeader + ": " + value + "\r\n")
	return append(prefix, message...), nil
}

func forwardingHops(message, key []byte) int {
	if len(key) == 0 {
		return 0
	}
	count := 0
	for name, values := range messageHeaders(message) {
		if !strings.EqualFold(name, forwardingHeader) {
			continue
		}
		for _, value := range values {
			parts := strings.Split(strings.TrimSpace(value), ".")
			if len(parts) != 3 || parts[0] != "v1" {
				continue
			}
			nonce, nonceErr := base64.RawURLEncoding.DecodeString(parts[1])
			signature, signatureErr := base64.RawURLEncoding.DecodeString(parts[2])
			if nonceErr != nil || signatureErr != nil || len(nonce) != 16 || len(signature) != sha256.Size {
				continue
			}
			mac := hmac.New(sha256.New, key)
			_, _ = mac.Write([]byte(markerContext + parts[1]))
			if hmac.Equal(signature, mac.Sum(nil)) {
				count++
			}
		}
	}
	return count
}

func receivedHops(message []byte) int {
	count := 0
	for name, values := range messageHeaders(message) {
		if strings.EqualFold(name, "Received") {
			count += len(values)
		}
	}
	return count
}
