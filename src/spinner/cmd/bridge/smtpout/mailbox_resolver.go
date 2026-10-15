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
)

type HTTPMailboxResolver struct {
	origin string
	token  string
	client *http.Client
}

func NewHTTPMailboxResolver(origin, token string) (*HTTPMailboxResolver, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) || token == "" {
		return nil, errors.New("private mailbox credential resolver is invalid")
	}
	return &HTTPMailboxResolver{origin: strings.TrimRight(origin, "/"), token: token,
		client: &http.Client{Timeout: RetryableLookupTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (r *HTTPMailboxResolver) Resolve(ctx context.Context, id string) (MailboxCredential, error) {
	if r == nil || !validConnectionID(id) {
		return MailboxCredential{}, errors.New("mailbox connection ID is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.origin+"/_internal/gateway/mailbox-connections/"+id+"/smtp", nil)
	if err != nil {
		return MailboxCredential{}, err
	}
	request.Header.Set("X-Gateway-Internal", r.token)
	response, err := r.client.Do(request)
	if err != nil {
		return MailboxCredential{}, errors.New("mailbox credential authority is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return MailboxCredential{}, fmt.Errorf("mailbox credential authority returned status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(body) > 16384 {
		return MailboxCredential{}, errors.New("mailbox credential response exceeds the limit")
	}
	var credential MailboxCredential
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credential); err != nil {
		return MailboxCredential{}, errors.New("mailbox credential response is invalid")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return MailboxCredential{}, errors.New("mailbox credential response exceeds the limit")
	}
	if credential.UpstreamID == "" || credential.Username == "" || credential.Secret == "" {
		return MailboxCredential{}, errors.New("mailbox credential response is incomplete")
	}
	return credential, nil
}

var _ MailboxCredentialResolver = (*HTTPMailboxResolver)(nil)
