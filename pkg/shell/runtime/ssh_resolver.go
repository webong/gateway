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

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/arranger/integration"
)

const maximumSSHCredentialDocument = 32768

var executionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type httpSSHResolver struct {
	origin string
	token  string
	client *http.Client
}

type reverseSSHResolver struct{ caller plugin.ReverseCaller }

func (resolver reverseSSHResolver) Resolve(ctx context.Context, id string) (SSHExecution, error) {
	reference := "ssh-" + id
	if resolver.caller == nil || !integration.ValidSecretReference(reference) {
		return SSHExecution{}, errors.New("SSH Shell credential authority is unavailable")
	}
	payload, err := json.Marshal(integration.SecretRequest{Reference: reference})
	if err != nil {
		return SSHExecution{}, errors.New("SSH Shell credential request is invalid")
	}
	response, err := resolver.caller.Call(ctx, integration.SecretContract, integration.OperationResolveSecret, payload)
	if err != nil {
		return SSHExecution{}, errors.New("SSH Shell credential authority is unavailable")
	}
	var secret integration.SecretValue
	if err := json.Unmarshal(response, &secret); err != nil || secret.Reference != reference || secret.Version < 1 || len(secret.Value) == 0 {
		return SSHExecution{}, errors.New("SSH Shell credential response is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(secret.Value)))
	decoder.DisallowUnknownFields()
	var execution SSHExecution
	if err := decoder.Decode(&execution); err != nil || decoder.Decode(new(any)) != io.EOF {
		return SSHExecution{}, errors.New("SSH Shell credential response is invalid")
	}
	if err := validateSSHExecution(execution); err != nil {
		return SSHExecution{}, err
	}
	return execution, nil
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
	reference := "ssh-" + id
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.origin+"/_internal/gateway/extensions/webong/shell/secrets/"+reference, nil)
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
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	var document struct {
		Reference string          `json:"reference"`
		Version   int64           `json:"version"`
		Value     json.RawMessage `json:"value"`
	}
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || decoder.Decode(new(any)) != io.EOF ||
		document.Reference != reference || document.Version < 1 || len(document.Value) == 0 {
		return SSHExecution{}, errors.New("SSH Shell credential response is invalid")
	}
	valueDecoder := json.NewDecoder(strings.NewReader(string(document.Value)))
	valueDecoder.DisallowUnknownFields()
	var execution SSHExecution
	if err := valueDecoder.Decode(&execution); err != nil || valueDecoder.Decode(new(any)) != io.EOF {
		return SSHExecution{}, errors.New("SSH Shell credential response is invalid")
	}
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
