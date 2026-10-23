package provision

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

// DeploymentTarget references credentials installed on the worker, not raw
// private keys supplied by the control plane. Connections are immutable.
type DeploymentTarget struct {
	ID         string           `json:"id"`
	Driver     string           `json:"driver"`
	Connection TargetConnection `json:"connection"`
}

type TargetConnection struct {
	Context     string `json:"context"`
	Kubeconfig  string `json:"kubeconfig"`
	Namespace   string `json:"namespace"`
	RouteMode   string `json:"route_mode"`
	PublishHost string `json:"publish_host"`
	RouteHost   string `json:"route_host"`
	Network     string `json:"network"`
}

func targetContext(target *DeploymentTarget) string {
	if target == nil {
		return ""
	}
	return target.Connection.Context
}

func (c *HTTPControlPlane) Target(ctx context.Context) (*DeploymentTarget, error) {
	if c.targetID == "" {
		return nil, fmt.Errorf("deployment target ID is required")
	}
	request, err := c.request(ctx, http.MethodGet, "/_internal/provisioning/targets/"+url.PathEscape(c.targetID), nil)
	if err != nil {
		return nil, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("load deployment target: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("load deployment target: status %d", response.StatusCode)
	}
	var target DeploymentTarget
	if err := decodeResponse(response.Body, &target); err != nil {
		return nil, err
	}
	if target.ID != c.targetID {
		return nil, fmt.Errorf("received a different deployment target")
	}
	return &target, nil
}

func (t DeploymentTarget) Check(ctx context.Context, config Config, runner commandRunner) error {
	switch t.Driver {
	case DriverLocal:
		return nil // There is no remote connection; workload binaries are checked at launch.
	case DriverDocker:
		if strings.TrimSpace(t.Connection.Context) == "" || net.ParseIP(t.Connection.PublishHost) == nil || net.ParseIP(t.Connection.RouteHost) == nil || net.ParseIP(t.Connection.RouteHost).IsUnspecified() {
			return fmt.Errorf("Docker target requires context, publish_host and reachable route_host IPs")
		}
		_, err := runner.Output(ctx, nil, config.DockerBinary, "--context", t.Connection.Context, "info", "--format", "{{.ID}}")
		return err
	case DriverKubernetes:
		if strings.TrimSpace(t.Connection.Context) == "" || !filepath.IsAbs(t.Connection.Kubeconfig) || t.Connection.Namespace == "" || (t.Connection.RouteMode != "pod-ip" && t.Connection.RouteMode != "port-forward") {
			return fmt.Errorf("Kubernetes target requires context, absolute kubeconfig path, namespace and route_mode")
		}
		prefix := KubernetesDriverConfig{Context: t.Connection.Context, Kubeconfig: t.Connection.Kubeconfig}.connectionArguments()
		prefix = append(prefix, "--namespace", t.Connection.Namespace)
		checks := [][]string{{"get", "pods"}, {"list", "pods"}, {"watch", "pods"}, {"create", "pods"}, {"delete", "pods"}, {"deletecollection", "pods"}}
		if t.Connection.RouteMode == "port-forward" {
			checks = append(checks, []string{"create", "pods", "--subresource=portforward"})
		}
		for _, check := range checks {
			args := append(append([]string{}, prefix...), "auth", "can-i")
			args = append(args, check...)
			answer, err := runner.Output(ctx, nil, config.KubernetesKubectl, args...)
			if err != nil || strings.TrimSpace(answer) != "yes" {
				return fmt.Errorf("Kubernetes target access check failed for %s %s", check[0], check[1])
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported deployment target driver %q", t.Driver)
	}
}

// CheckTarget performs only reads and permission checks. It does not register a
// node, reset instance state, or prepare (delete) existing workloads.
func CheckTarget(ctx context.Context, config Config, backendURL string, client *http.Client) error {
	control, err := NewHTTPControlPlane(backendURL, config.TargetToken, client)
	if err != nil {
		return err
	}
	control.targetID = config.TargetID
	target, err := control.Target(ctx)
	if err != nil {
		return err
	}
	return target.Check(ctx, config, execCommandRunner{})
}
