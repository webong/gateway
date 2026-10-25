// Command spinner is Web's installable Gateway Extension entrypoint.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	runtimecontract "github.com/webong/gateway/arranger/runtime"
	web "github.com/webong/gateway/pkg/web/spinner"
	"github.com/webong/gateway/pkg/web/spinner/websocket"
	"github.com/webong/gateway/pkg/web/spinner/webstream"
)

func main() {
	if err := runtimecontract.Serve(context.Background(), runtimecontract.StarterFunc(start)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func start(_ context.Context, request runtimecontract.StartRequest, caller plugin.ReverseCaller) (runtimecontract.StartResponse, error) {
	host := runtimecontract.NewHost(caller, request.AutomationLanguages)
	edge := web.NewEdge(host.Planner(), host.Executor(), request.MaxBodySize).
		SetAutomationExecutor(host.Automation()).
		SetScheduleRegistrar(host.Schedules()).
		SetPassThrough(host.PassThrough())
	websocketHandler := websocket.NewHandler(host.ProtocolPlanner(), host.Executor(), websocket.Config{
		MaxMessageSize: request.MaxBodySize, PlannerTimeout: request.PlannerTimeout,
		MaxConnections: request.WebSocketMaxConnections, MaxDuration: request.WebSocketMaxDuration,
	})
	webstreamHandler := webstream.NewHandler(host.ProtocolPlanner(), web.PublicWebstreamSource{}, webstream.Config{
		MaxBodySize: request.MaxBodySize, PlannerTimeout: request.PlannerTimeout,
		HeartbeatInterval: request.WebstreamHeartbeat, ReconnectDelay: request.WebstreamRetry,
		MaxConnections: request.WebstreamMaxConnections, MaxDuration: request.WebstreamMaxDuration,
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
		return runtimecontract.StartResponse{}, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: request.PlannerTimeout}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	return runtimecontract.StartResponse{HTTP: []runtimecontract.HTTPMount{{
		Prefix: "/", Address: "http://" + listener.Addr().String(),
	}}}, nil
}
