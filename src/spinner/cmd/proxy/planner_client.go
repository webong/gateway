package main

import (
	"context"
	"fmt"
	"net"
	"net/http"

	relayconfig "github.com/webong/gateway/src/spinner/internal/config"
)

// newPlannerClient preserves the HTTP planner contract while allowing a
// colocated Planner to be reached through a Unix domain socket. The Planner
// URL still supplies the HTTP Host header and optional path prefix.
func newPlannerClient(config *relayconfig.Config) *http.Client {
	client := &http.Client{Timeout: config.RequestTimeout}
	if config.PlannerSocket == "" {
		return client
	}

	dialer := &net.Dialer{}
	client.Transport = &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", config.PlannerSocket)
		},
		ForceAttemptHTTP2: false,
	}

	return client
}

// newPlannerReadinessCheck uses the same private transport as route planning,
// but refuses redirects and accepts only the Planner's no-content ready reply.
func newPlannerReadinessCheck(readyURL, internalToken string, client *http.Client) func(context.Context) error {
	probe := *client
	probe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return func(ctx context.Context) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, readyURL, nil)
		if err != nil {
			return err
		}
		request.Header.Set("X-Gateway-Internal", internalToken)
		response, err := probe.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			return fmt.Errorf("Planner readiness returned HTTP %d", response.StatusCode)
		}
		return nil
	}
}
