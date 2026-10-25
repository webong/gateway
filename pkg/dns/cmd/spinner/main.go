// Command spinner is DNS's installable Gateway Extension entrypoint.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	runtimecontract "github.com/webong/gateway/arranger/integration"
	dns "github.com/webong/gateway/pkg/dns/spinner"
)

type configuration struct {
	Address            string     `json:"address"`
	Zones              []dns.Zone `json:"zones"`
	PlannerTimeoutMS   int        `json:"planner_timeout_ms,omitempty"`
	ReadTimeoutMS      int        `json:"read_timeout_ms,omitempty"`
	WriteTimeoutMS     int        `json:"write_timeout_ms,omitempty"`
	MaxResponseRecords int        `json:"max_response_records,omitempty"`
	QueriesPerSecond   int        `json:"queries_per_second,omitempty"`
	QueryBurst         int        `json:"query_burst,omitempty"`
}

func main() {
	if err := runtimecontract.ServeRuntime(context.Background(), runtimecontract.RuntimeStarterFunc(start)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func start(_ context.Context, request runtimecontract.RuntimeStartRequest, caller plugin.ReverseCaller) (runtimecontract.RuntimeStartResponse, error) {
	var configured configuration
	if len(request.Configuration) == 0 {
		return runtimecontract.RuntimeStartResponse{}, errors.New("DNS runtime configuration is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Configuration))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configured); err != nil {
		return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("decode DNS runtime configuration: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return runtimecontract.RuntimeStartResponse{}, errors.New("DNS runtime configuration contains trailing data")
	}

	host := runtimecontract.NewRuntimeHost(caller, nil)
	server, err := dns.NewServer(dns.Config{
		Address: configured.Address, Zones: configured.Zones,
		PlannerTimeout: durationMS(configured.PlannerTimeoutMS),
		ReadTimeout:    durationMS(configured.ReadTimeoutMS), WriteTimeout: durationMS(configured.WriteTimeoutMS),
		MaxResponseRecords: configured.MaxResponseRecords,
		RateLimit:          configured.QueriesPerSecond, RateBurst: configured.QueryBurst,
	}, host.ProtocolPlanner(), host.Executor())
	if err != nil {
		return runtimecontract.RuntimeStartResponse{}, err
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, dns.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, err)
		}
	}()

	return runtimecontract.RuntimeStartResponse{}, nil
}

func durationMS(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Millisecond
}
