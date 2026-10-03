package provision

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

const maxControlResponseSize = 4 * 1024 * 1024

var ErrNodeSessionActive = errors.New("another worker session owns this provisioning node")

type ControlPlane interface {
	Assignments(context.Context, string, []string) ([]ServerSpec, string, error)
	Report(context.Context, string, string, InstanceReport) error
	ResetNode(context.Context, string) error
}

type HTTPControlPlane struct {
	client    *http.Client
	base      *url.URL
	token     string
	targetID  string
	sessionID string
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
		return nil, fmt.Errorf("a provisioning credential is required (target token or legacy internal token)")
	}
	if client == nil {
		client = http.DefaultClient
	}

	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("generate provisioning worker session: %w", err)
	}
	return &HTTPControlPlane{client: client, base: backend, token: token, sessionID: hex.EncodeToString(random[:])}, nil
}

func (c *HTTPControlPlane) Assignments(ctx context.Context, nodeID string, drivers []string) ([]ServerSpec, string, error) {
	payload, err := json.Marshal(map[string]any{"drivers": drivers, "session_id": c.sessionID})
	if err != nil {
		return nil, "", fmt.Errorf("encode provisioning node heartbeat: %w", err)
	}
	request, err := c.request(ctx, http.MethodPut, "/_internal/provisioning/nodes/"+url.PathEscape(nodeID)+"/assignments", bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("load provisioned assignments: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusLocked {
			return nil, "", ErrNodeSessionActive
		}
		return nil, "", fmt.Errorf("load provisioned assignments: status %d", response.StatusCode)
	}

	var document struct {
		Data []ServerSpec `json:"data"`
		Meta struct {
			LeaderID  string `json:"leader_id"`
			SessionID string `json:"session_id"`
		} `json:"meta"`
	}
	if err := decodeResponse(response.Body, &document); err != nil {
		return nil, "", fmt.Errorf("decode provisioned assignments: %w", err)
	}
	if document.Meta.SessionID != c.sessionID {
		return nil, "", fmt.Errorf("Planner did not confirm provisioning worker session; upgrade Planner before using this Spinner")
	}
	for _, spec := range document.Data {
		if spec.TargetID != c.targetID {
			return nil, "", fmt.Errorf("received assignment for a different deployment target")
		}
		if err := spec.Validate(); err != nil {
			return nil, "", err
		}
	}

	return document.Data, document.Meta.LeaderID, nil
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

// ReleaseNode permits an immediate graceful handover. A crashed worker cannot
// release; its session remains fenced until the Planner coordination lease
// expires.
func (c *HTTPControlPlane) ReleaseNode(ctx context.Context, nodeID string) error {
	payload, err := json.Marshal(map[string]string{"session_id": c.sessionID})
	if err != nil {
		return err
	}
	request, err := c.request(ctx, http.MethodDelete, "/_internal/provisioning/nodes/"+url.PathEscape(nodeID)+"/session", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("release provisioning node session: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("release provisioning node session: status %d", response.StatusCode)
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
	request.Header.Set("X-Gateway-Worker-Session", c.sessionID)
	if c.targetID != "" {
		request.Header.Set("X-Gateway-Target", c.targetID)
	}

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
