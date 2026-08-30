package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const maxControlResponseSize = 4 * 1024 * 1024

type ControlPlane interface {
	Servers(context.Context) ([]ServerSpec, error)
	Report(context.Context, string, string, InstanceReport) error
	ResetNode(context.Context, string) error
}

type HTTPControlPlane struct {
	client *http.Client
	base   *url.URL
	token  string
}

func NewHTTPControlPlane(backendURL, token string, client *http.Client) (*HTTPControlPlane, error) {
	backend, err := url.Parse(strings.TrimSpace(backendURL))
	if err != nil || backend.Scheme == "" || backend.Host == "" {
		return nil, fmt.Errorf("invalid provisioning control-plane URL %q", backendURL)
	}
	if backend.Scheme != "http" && backend.Scheme != "https" {
		return nil, fmt.Errorf("provisioning control-plane URL must use http or https")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("GATEWAY_INTERNAL_TOKEN is required for provisioning reconciliation")
	}
	if client == nil {
		client = http.DefaultClient
	}

	return &HTTPControlPlane{client: client, base: backend, token: token}, nil
}

func (c *HTTPControlPlane) Servers(ctx context.Context) ([]ServerSpec, error) {
	request, err := c.request(ctx, http.MethodGet, "/_internal/provisioning/servers", nil)
	if err != nil {
		return nil, err
	}

	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("load provisioned server specifications: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("load provisioned server specifications: status %d", response.StatusCode)
	}

	var document struct {
		Data []ServerSpec `json:"data"`
	}
	if err := decodeResponse(response.Body, &document); err != nil {
		return nil, fmt.Errorf("decode provisioned server specifications: %w", err)
	}
	for _, spec := range document.Data {
		if err := spec.Validate(); err != nil {
			return nil, err
		}
	}

	return document.Data, nil
}

func (c *HTTPControlPlane) Report(ctx context.Context, serverID, instanceID string, report InstanceReport) error {
	payload, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("encode provisioned instance report: %w", err)
	}
	endpoint := "/_internal/provisioning/servers/" + url.PathEscape(serverID) + "/instances/" + url.PathEscape(instanceID)
	request, err := c.request(ctx, http.MethodPut, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("report provisioned instance: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("report provisioned instance: status %d", response.StatusCode)
	}

	return nil
}

func (c *HTTPControlPlane) ResetNode(ctx context.Context, nodeID string) error {
	payload, err := json.Marshal(map[string]string{"node_id": nodeID})
	if err != nil {
		return fmt.Errorf("encode provisioning node reset: %w", err)
	}
	request, err := c.request(ctx, http.MethodPost, "/_internal/provisioning/instances/reset", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("reset provisioned node instances: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("reset provisioned node instances: status %d", response.StatusCode)
	}

	return nil
}

func (c *HTTPControlPlane) request(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	target := *c.base
	target.Path = strings.TrimSuffix(c.base.Path, "/") + path.Clean("/"+endpoint)
	target.RawQuery = ""
	target.Fragment = ""

	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create provisioning control-plane request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Gateway-Internal", c.token)

	return request, nil
}

func decodeResponse(reader io.Reader, target any) error {
	limited := io.LimitReader(reader, maxControlResponseSize+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if len(payload) > maxControlResponseSize {
		return fmt.Errorf("response exceeds %d bytes", maxControlResponseSize)
	}

	return json.Unmarshal(payload, target)
}

var _ ControlPlane = (*HTTPControlPlane)(nil)
