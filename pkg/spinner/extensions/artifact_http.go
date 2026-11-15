package extensions

import (
	"errors"
	"net/http"
	"time"
)

// NewArtifactHTTPClient fetches checksum-pinned extension archives through at
// most five HTTPS redirects (for example release-origin to CDN). TLS remains
// verified and Planner credentials use a separate client.
func NewArtifactHTTPClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("extension artifact exceeded HTTPS redirect limit")
		}
		if request.URL.Scheme != "https" || request.URL.Host == "" || request.URL.User != nil || request.URL.Fragment != "" {
			return errors.New("extension artifact redirect requires an HTTPS URL without user information or fragment")
		}
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		request.Header.Del("Referer")
		return nil
	}}
}
