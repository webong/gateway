package mailpoll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
)

var ErrUnavailable = errors.New("mailbox poll is unavailable or already leased")

// HandlerControlPlane invokes private Planner routes in-process. Credentials
// never traverse the public Router or a second network listener.
type HandlerControlPlane struct {
	handler http.Handler
	token   string
	prefix  string
}

func NewHandlerControlPlane(handler http.Handler, token string) (*HandlerControlPlane, error) {
	if handler == nil || token == "" {
		return nil, fmt.Errorf("private mailbox poll Planner requires a handler and token")
	}
	return &HandlerControlPlane{handler: handler, token: token, prefix: "/_internal/gateway/mailbox-polls/"}, nil
}

// NewExtensionControlPlane uses the identity-scoped private Planner
// capability granted to the installed Email Extension. The host injects its
// internal credential; Email never receives it.
func NewExtensionControlPlane(handler http.Handler) (*HandlerControlPlane, error) {
	if handler == nil {
		return nil, fmt.Errorf("private mailbox poll Planner requires a handler")
	}
	return &HandlerControlPlane{handler: handler, prefix: "/_internal/gateway/extensions/webong/email/mailbox-polls/"}, nil
}

func (c *HandlerControlPlane) Claim(ctx context.Context, routeID string) (Claim, error) {
	var result Claim
	err := c.call(ctx, routeID, "claim", map[string]any{}, &result)
	return result, err
}

func (c *HandlerControlPlane) Ack(ctx context.Context, routeID, token string, validity, uid uint32) error {
	return c.call(ctx, routeID, "ack", map[string]any{
		"lease_token": token, "uid_validity": validity, "uid": uid,
	}, nil)
}

func (c *HandlerControlPlane) Release(ctx context.Context, routeID, token string) error {
	return c.call(ctx, routeID, "release", map[string]any{"lease_token": token}, nil)
}

func (c *HandlerControlPlane) call(ctx context.Context, routeID, action string, input any, output any) error {
	if _, err := url.ParseRequestURI("/" + routeID); err != nil || routeID == "" {
		return fmt.Errorf("invalid poll route ID")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://gateway.internal"+c.prefix+url.PathEscape(routeID)+"/"+action,
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if c.token != "" {
		request.Header.Set("X-Gateway-Internal", c.token)
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if action == "claim" && response.StatusCode == http.StatusConflict {
			return ErrUnavailable
		}
		return fmt.Errorf("mailbox poll %s returned HTTP %d", action, response.StatusCode)
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(output)
}
