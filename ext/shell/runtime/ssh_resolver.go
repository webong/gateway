package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maximumSSHCredentialDocument = 32768

var executionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type httpSSHResolver struct {
	origin string
	token  string
	client *http.Client
}

func (e *Engine) SetSSHResolverHTTP(origin, token string, client *http.Client, localTransport bool) error {
	if e == nil || len(e.sshAllowed) == 0 {
		return nil
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !(parsed.Scheme == "http" && (localTransport || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost"))) || token == "" {
		return errors.New("private SSH Shell credential resolver is invalid")
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	privateClient := *client
	privateClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if privateClient.Timeout <= 0 || privateClient.Timeout > 5*time.Second {
		privateClient.Timeout = 5 * time.Second
	}
	e.SetSSHResolver(&httpSSHResolver{origin: strings.TrimRight(origin, "/"), token: token, client: &privateClient})
	return nil
}

func (e *Engine) SetSSHResolverHandler(handler http.Handler, token string) error {
	if e == nil || len(e.sshAllowed) == 0 {
		return nil
	}
	resolver, err := newHandlerSSHResolver(handler, token)
	if err != nil {
		return err
	}
	e.SetSSHResolver(resolver)
	return nil
}

func (e *Engine) ContextWithSSHResolverHandler(ctx context.Context, handler http.Handler, token string) (context.Context, error) {
	if e == nil || len(e.sshAllowed) == 0 {
		return ctx, nil
	}
	resolver, err := newHandlerSSHResolver(handler, token)
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, sshResolverContextKey{}, resolver), nil
}

func newHandlerSSHResolver(handler http.Handler, token string) (SSHResolver, error) {
	if handler == nil || token == "" {
		return nil, errors.New("embedded SSH Shell credential resolver is invalid")
	}
	client := &http.Client{Transport: sshHandlerTransport{handler: handler}, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &httpSSHResolver{origin: "http://gateway.internal", token: token, client: client}, nil
}

func (r *httpSSHResolver) Resolve(ctx context.Context, id string) (SSHExecution, error) {
	if r == nil || !executionIDPattern.MatchString(id) {
		return SSHExecution{}, errors.New("SSH Shell execution reference is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.origin+"/_internal/gateway/shell/executions/"+id, nil)
	if err != nil {
		return SSHExecution{}, err
	}
	request.Header.Set("X-Gateway-Internal", r.token)
	response, err := r.client.Do(request)
	if err != nil {
		return SSHExecution{}, errors.New("SSH Shell credential authority is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return SSHExecution{}, errors.New("SSH Shell execution is unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumSSHCredentialDocument+1))
	if err != nil || len(body) > maximumSSHCredentialDocument {
		return SSHExecution{}, errors.New("SSH Shell credential response exceeds limit")
	}
	var execution SSHExecution
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	// Planner may include a credential version, but no arbitrary fields.
	var document struct {
		SSHExecution
		CredentialVersion int `json:"credential_version"`
	}
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || decoder.Decode(new(any)) != io.EOF {
		return SSHExecution{}, errors.New("SSH Shell credential response is invalid")
	}
	execution = document.SSHExecution
	if err := validateSSHExecution(execution); err != nil {
		return SSHExecution{}, err
	}
	return execution, nil
}

type sshHandlerTransport struct{ handler http.Handler }

func (t sshHandlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}
