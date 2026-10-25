package smtpout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPDKIMSigner resolves the current per-domain key over the private Planner
// connection on every check/sign. Revocation and rotation need no restart.
type HTTPDKIMSigner struct {
	origin string
	token  string
	client *http.Client
}

func NewHTTPDKIMSigner(origin, token string) (*HTTPDKIMSigner, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) || token == "" {
		return nil, errors.New("private DKIM signer resolver is invalid")
	}
	return &HTTPDKIMSigner{origin: strings.TrimRight(origin, "/"), token: token,
		client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (r *HTTPDKIMSigner) CheckSigningDomain(domain string) error {
	if r == nil || normalizedSigningDomain(domain) != domain || domain == "" {
		return errors.New("DKIM signing domain is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead,
		r.origin+"/_internal/gateway/email/dkim/"+domain, nil)
	if err != nil {
		return err
	}
	request.Header.Set("X-Gateway-Internal", r.token)
	response, err := r.client.Do(request)
	if err != nil {
		return errors.New("DKIM key authority is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("DKIM key authority returned status %d", response.StatusCode)
	}
	return nil
}

func (r *HTTPDKIMSigner) Sign(domain string, data []byte) ([]byte, error) {
	signer, err := r.resolve(domain)
	if err != nil {
		return nil, err
	}
	return signer.Sign(domain, data)
}

func (r *HTTPDKIMSigner) resolve(domain string) (*DKIMSigner, error) {
	if r == nil || normalizedSigningDomain(domain) != domain || domain == "" {
		return nil, errors.New("DKIM signing domain is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.origin+"/_internal/gateway/email/dkim/"+domain, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-Gateway-Internal", r.token)
	response, err := r.client.Do(request)
	if err != nil {
		return nil, errors.New("DKIM key authority is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DKIM key authority returned status %d", response.StatusCode)
	}
	var document struct {
		Domain        string `json:"domain"`
		Selector      string `json:"selector"`
		PrivateKeyPEM string `json:"private_key_pem"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 20001))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || decoder.Decode(new(any)) != io.EOF || document.Domain != domain {
		return nil, errors.New("DKIM key authority returned an invalid resource")
	}
	return NewDKIMSignerFromPEM(domain, document.Selector, document.PrivateKeyPEM)
}

var _ DKIMSigning = (*HTTPDKIMSigner)(nil)
