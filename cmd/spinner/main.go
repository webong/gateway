// Command spinner is Web's installable Gateway Extension entrypoint.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	runtimecontract "github.com/webong/gateway/arranger/integration"
	web "github.com/webong/gateway/pkg/web/spinner"
	"github.com/webong/gateway/pkg/web/spinner/websocket"
	"github.com/webong/gateway/pkg/web/spinner/webstream"
)

type configuration struct {
	WebSocket webSocketConfiguration `json:"websocket"`
	Webstream webstreamConfiguration `json:"webstream"`
}

type webSocketConfiguration struct {
	MaxConnections int `json:"max_connections"`
	MaxDurationMS  int `json:"max_duration_ms"`
}

type webstreamConfiguration struct {
	HeartbeatMS         int      `json:"heartbeat_ms"`
	RetryMS             int      `json:"retry_ms"`
	MaxConnections      int      `json:"max_connections"`
	MaxDurationMS       int      `json:"max_duration_ms"`
	PrivateNetworkCIDRs []string `json:"private_network_cidrs,omitempty"`
}

func main() {
	if err := runtimecontract.ServeRuntime(context.Background(), runtimecontract.RuntimeStarterFunc(start)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func start(_ context.Context, request runtimecontract.RuntimeStartRequest, caller plugin.ReverseCaller) (runtimecontract.RuntimeStartResponse, error) {
	configured, err := decodeConfiguration(request.Configuration)
	if err != nil {
		return runtimecontract.RuntimeStartResponse{}, err
	}
	host := runtimecontract.NewRuntimeHost(caller, request.AutomationLanguages)
	edge := web.NewEdge(host.Planner(), host.Executor(), request.MaxBodySize).
		SetAutomationExecutor(host.Automation()).
		SetScheduleRegistrar(host.Schedules()).
		SetPassThrough(host.PassThrough())
	websocketHandler := websocket.NewHandler(host.ProtocolPlanner(), host.Executor(), websocket.Config{
		MaxMessageSize: request.MaxBodySize, PlannerTimeout: request.PlannerTimeout,
		MaxConnections: configured.WebSocket.MaxConnections, MaxDuration: durationMS(configured.WebSocket.MaxDurationMS),
	})
	webstreamSource, err := web.NewWebstreamSource(configured.Webstream.PrivateNetworkCIDRs)
	if err != nil {
		return runtimecontract.RuntimeStartResponse{}, err
	}
	webstreamHandler := webstream.NewHandler(host.ProtocolPlanner(), webstreamSource, webstream.Config{
		MaxBodySize: request.MaxBodySize, PlannerTimeout: request.PlannerTimeout,
		HeartbeatInterval: durationMS(configured.Webstream.HeartbeatMS), ReconnectDelay: durationMS(configured.Webstream.RetryMS),
		MaxConnections: configured.Webstream.MaxConnections, MaxDuration: durationMS(configured.Webstream.MaxDurationMS),
	})
	handler := http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		switch {
		case websocket.IsUpgrade(incoming):
			websocketHandler.ServeHTTP(writer, incoming)
		case webstream.IsRequest(incoming):
			webstreamHandler.ServeHTTP(writer, incoming)
		default:
			edge.ServeHTTP(writer, incoming)
		}
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return runtimecontract.RuntimeStartResponse{}, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: request.PlannerTimeout}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	return runtimecontract.RuntimeStartResponse{HTTP: []runtimecontract.HTTPMount{{
		Prefix: "/", Address: "http://" + listener.Addr().String(),
	}}}, nil
}

func decodeConfiguration(raw json.RawMessage) (configuration, error) {
	if len(raw) == 0 {
		return configuration{}, errors.New("Web runtime configuration is required")
	}
	var configured configuration
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configured); err != nil {
		return configuration{}, fmt.Errorf("decode Web runtime configuration: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return configuration{}, errors.New("Web runtime configuration contains trailing data")
	}
	if configured.WebSocket.MaxConnections < 1 || configured.WebSocket.MaxDurationMS < 1 ||
		configured.Webstream.MaxConnections < 1 || configured.Webstream.MaxDurationMS < 1 ||
		configured.Webstream.HeartbeatMS < 0 || configured.Webstream.RetryMS < 1 {
		return configuration{}, errors.New("Web runtime limits are invalid")
	}
	return configured, nil
}

func durationMS(value int) time.Duration { return time.Duration(value) * time.Millisecond }
