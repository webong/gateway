package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	smtpclient "github.com/emersion/go-smtp"
	mdns "github.com/miekg/dns"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	runtimecontract "github.com/webong/gateway/arranger/integration"
	emailruntime "github.com/webong/gateway/pkg/email/spinner"
)

type reverseCallerFunc func(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)

func (f reverseCallerFunc) Call(ctx context.Context, ref plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
	return f(ctx, ref, operation, payload)
}

func TestSMTPRuntimeUsesResolvedDNSCapability(t *testing.T) {
	dnsCalls := 0
	plannerCalls := 0
	caller := reverseCallerFunc(func(_ context.Context, ref plugin.ContractRef, operation string, payload json.RawMessage) (json.RawMessage, error) {
		switch {
		case ref == runtimecontract.DNSContract && operation == runtimecontract.OperationDNSExchange:
			dnsCalls++
			var input runtimecontract.DNSExchangeRequest
			if err := json.Unmarshal(payload, &input); err != nil {
				return nil, err
			}
			query := new(mdns.Msg)
			if err := query.Unpack(input.Message); err != nil {
				return nil, err
			}
			response := new(mdns.Msg)
			response.SetReply(query)
			response.RecursionAvailable = true
			wire, err := response.Pack()
			if err != nil {
				return nil, err
			}
			return json.Marshal(runtimecontract.DNSExchangeResponse{Message: wire})
		case ref == runtimecontract.PlannerContract && operation == runtimecontract.OperationPlanEvent:
			plannerCalls++
			return json.Marshal(runtimecontract.GatewayDecision{
				Version: runtimecontract.ProtocolVersion, Protocol: runtimecontract.ProtocolSMTP,
				Action: runtimecontract.GatewayAccept,
			})
		default:
			return nil, fmt.Errorf("unexpected reverse call %s@%s/%s", ref.Name, ref.Version, operation)
		}
	})
	host := runtimecontract.NewRuntimeHost(caller, nil)
	resolver, err := emailruntime.NewDNSResolver(host.DNS())
	if err != nil {
		t.Fatal(err)
	}
	server, err := newSMTPServer(smtpConfiguration{
		Address: "127.0.0.1:0", Hostname: "smtp.example.test",
		DNSLookupTimeoutMS: 1000,
	}, host, resolver)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-serveDone
	})

	client, err := smtpclient.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	message := []byte("From: sender@example.test\r\nTo: inbox@example.test\r\nSubject: dependency smoke\r\n\r\nbody\r\n")
	if err := client.SendMail("sender@example.test", []string{"inbox@example.test"}, bytes.NewReader(message)); err != nil {
		t.Fatal(err)
	}
	if dnsCalls == 0 || plannerCalls != 1 {
		t.Fatalf("SMTP runtime calls: dns=%d planner=%d", dnsCalls, plannerCalls)
	}
}
