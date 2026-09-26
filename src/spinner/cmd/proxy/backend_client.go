package main

import (
	"context"
	"net"
	"net/http"

	relayconfig "github.com/webong/gateway/src/spinner/internal/config"
)

// newLaravelBackendClient preserves the HTTP planner contract while allowing a
// colocated PHP server to be reached through a Unix domain socket. The backend
// URL still supplies the HTTP Host header and optional path prefix.
func newLaravelBackendClient(config *relayconfig.Config) *http.Client {
	client := &http.Client{Timeout: config.RequestTimeout}
	if config.LaravelBackendSocket == "" {
		return client
	}

	dialer := &net.Dialer{}
	client.Transport = &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", config.LaravelBackendSocket)
		},
		ForceAttemptHTTP2: false,
	}

	return client
}
