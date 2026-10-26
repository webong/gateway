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
	emailattrs "github.com/webong/gateway/pkg/email/spinner/attributes"
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

func TestEmailRuntimeAdvertisesItsFailureHandler(t *testing.T) {
	runtime := new(runtime)
	response, err := runtime.start(context.Background(), runtimecontract.RuntimeStartRequest{
		Configuration: json.RawMessage(`{"failure_notice_domain":"gateway.example.test"}`),
		MaxBodySize:   1024 * 1024,
		InstanceKey:   []byte("extension-scoped-test-key"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.DeliveryFailureHandlers) != 4 {
		t.Fatalf("failure handlers = %v", response.DeliveryFailureHandlers)
	}
	payload, err := json.Marshal(runtimecontract.DeliveryFailureRequest{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: time.Now(), FailedAt: time.Now(),
		Delivery: runtimecontract.GatewayDelivery{
			Protocol: runtimecontract.ProtocolSMTP, Adapter: "smtp", Target: "buyer@example.test",
			Attributes: map[string]string{"mail_from": "sender@example.test", emailattrs.FailureNoticeTo: "sender@example.test"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.handle(context.Background(), plugin.Request{
		Contract: runtimecontract.DeliveryFailureContract, Operation: runtimecontract.OperationDeliveryFailure, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	var followUp runtimecontract.DeliveryFailureResponse
	if err := json.Unmarshal(result, &followUp); err != nil || followUp.FollowUp == nil || followUp.FollowUp.Adapter != "smtp" {
		t.Fatalf("failure response = %+v, %v", followUp, err)
	}
}

func TestEmailRuntimeAdvertisesSeparatePublicAndPrivateSubmissionMounts(t *testing.T) {
	response, err := new(runtime).start(context.Background(), runtimecontract.RuntimeStartRequest{
		Configuration: json.RawMessage(`{"smtp_relay":{"address":"relay.example.test:2525","tls_mode":"none"}}`),
		MaxBodySize:   1024 * 1024,
		InstanceKey:   []byte("extension-scoped-test-key"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.HTTP) != 1 || len(response.PrivateHTTP) != 1 ||
		response.HTTP[0].Prefix != emailruntime.SubmissionPath || response.PrivateHTTP[0].Prefix != emailruntime.SubmissionPath ||
		response.HTTP[0].Address == response.PrivateHTTP[0].Address {
		t.Fatalf("submission mounts = public:%+v private:%+v", response.HTTP, response.PrivateHTTP)
	}
	if fmt.Sprint(response.DeliveryAdapters) != "[mailbox.smtp managed.smtp smtp]" {
		t.Fatalf("delivery adapters = %v", response.DeliveryAdapters)
	}
}

func TestEmailRuntimeAdvertisesPlannerManagedProvidersWithoutEmbeddingTheirSecrets(t *testing.T) {
	response, err := new(runtime).start(context.Background(), runtimecontract.RuntimeStartRequest{
		Configuration: json.RawMessage(`{"hosted_mail":{"allowed_hosts":["provider.example.test"]}}`),
		MaxBodySize:   1024 * 1024,
		InstanceKey:   []byte("extension-scoped-test-key"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(response.DeliveryAdapters) != "[hosted.mail mailbox.smtp managed.smtp]" {
		t.Fatalf("delivery adapters = %v", response.DeliveryAdapters)
	}
}

func TestEmailRuntimeAcceptsOnlyAReferenceForCredentialedRelay(t *testing.T) {
	response, err := new(runtime).start(context.Background(), runtimecontract.RuntimeStartRequest{
		Configuration: json.RawMessage(`{"smtp_relay":{"address":"relay.example.test:587","tls_mode":"starttls","credential_ref":"primary-relay"}}`),
		MaxBodySize:   1024 * 1024,
		InstanceKey:   []byte("extension-scoped-test-key"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(response.DeliveryAdapters) != "[mailbox.smtp managed.smtp smtp]" {
		t.Fatalf("delivery adapters = %v", response.DeliveryAdapters)
	}
}

func TestEmailRuntimeRejectsRelayCredentialsInNonSecretConfiguration(t *testing.T) {
	_, err := new(runtime).start(context.Background(), runtimecontract.RuntimeStartRequest{
		Configuration: json.RawMessage(`{"smtp_relay":{"address":"relay.example.test:587","tls_mode":"starttls","password":"do-not-store-here"}}`),
	}, nil)
	if err == nil {
		t.Fatal("Email runtime accepted a relay credential in non-secret Extension configuration")
	}
}
