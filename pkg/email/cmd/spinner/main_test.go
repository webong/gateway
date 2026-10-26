package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	smtpclient "github.com/emersion/go-smtp"
	mdns "github.com/miekg/dns"
	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host"
	"github.com/webong/ext/pkg/plugin-go/contract/host/process"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
	runtimecontract "github.com/webong/gateway/arranger/integration"
	emailruntime "github.com/webong/gateway/pkg/email/spinner"
	emailattrs "github.com/webong/gateway/pkg/email/spinner/attributes"
	"github.com/webong/gateway/pkg/email/spinner/smtpout"
)

const emailRuntimeGuestEnv = "GATEWAY_EMAIL_RUNTIME_GUEST"

// TestEmailRuntimeGuestProcess is the process entrypoint used by the fresh-child
// mailbox regression below. The child serves the same runtime as the packaged
// Email Spinner executable.
func TestEmailRuntimeGuestProcess(t *testing.T) {
	if os.Getenv(emailRuntimeGuestEnv) != "1" {
		return
	}
	guest := new(runtime)
	err := runtimecontract.ServeRuntimeWithHandler(context.Background(),
		runtimecontract.RuntimeStarterFunc(guest.start), runtimecontract.RuntimeOperationHandlerFunc(guest.handle))
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		fmt.Fprintf(os.Stderr, "Email runtime guest exited: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestFreshEmailProcessHandlesOAuthMailboxCredentialLookup(t *testing.T) {
	identity := plugin.Identity{ID: "webong/email", Revision: "test-r1", Version: "0.1.0"}
	descriptor := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   identity,
		Contracts: []plugin.Contract{
			{ContractRef: runtimecontract.RuntimeContract, Operations: []plugin.Operation{{Name: runtimecontract.OperationStart, Surface: "spinner"}}},
			{ContractRef: runtimecontract.DeliveryAdapterContract, Operations: []plugin.Operation{{Name: runtimecontract.OperationDeliverAdapter, Surface: "spinner"}}},
		},
		Reverse: []plugin.Contract{{
			ContractRef: runtimecontract.PlannerContract,
			Operations:  []plugin.Operation{{Name: runtimecontract.OperationPrivatePassThrough}},
		}},
	}
	packageDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(packageDir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	stub := []byte("verified test artifact")
	if err := os.WriteFile(filepath.Join(packageDir, "bin", "spinner"), stub, 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(stub)
	manifest := packagekit.Manifest{
		APIVersion:  packagekit.Version,
		Descriptor:  descriptor,
		Artifacts:   []packagekit.Artifact{{Name: "spinner", Path: "bin/spinner", SHA256: hex.EncodeToString(digest[:])}},
		Entrypoints: []packagekit.Entrypoint{{Name: "spinner", Runtime: "process", Artifact: "spinner", Protocols: []string{plugin.APIVersion}}},
	}
	if err := store.WriteManifest(packageDir, manifest); err != nil {
		t.Fatal(err)
	}

	mailboxID := "11111111-1111-4111-8111-111111111111"
	credential := smtpout.MailboxCredential{
		UpstreamID: "gmail", Username: "sender@gmail.com", Secret: "short-lived-token",
		Mechanism: "XOAUTH2", Address: "sender@gmail.com", UpstreamAddress: "smtp.invalid:587",
		UpstreamServerName: "smtp.invalid", UpstreamTLSMode: "starttls", UpstreamNetworkScope: "public",
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	passThroughCalls := 0
	reverse := &plugin.ReverseOptions{
		Authorize: func(_ context.Context, request plugin.Request) error {
			if request.Contract != runtimecontract.PlannerContract || request.Operation != runtimecontract.OperationPrivatePassThrough {
				return plugin.ErrDenied
			}
			return nil
		},
		Handle: func(_ context.Context, request plugin.Request) (json.RawMessage, error) {
			var input runtimecontract.PassThroughRequest
			if err := plugin.Decode(request.Payload, &input); err != nil {
				return nil, err
			}
			if input.Method != http.MethodGet || input.Path != "/_internal/gateway/extensions/webong/email/mailbox-connections/"+mailboxID+"/smtp" {
				return nil, fmt.Errorf("unexpected private Planner request %s %s", input.Method, input.Path)
			}
			passThroughCalls++
			body, err := json.Marshal(runtimecontract.PassThroughResponse{Status: http.StatusOK, Body: credentialJSON})
			return body, err
		},
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	installed := store.Installed{Manifest: manifest, Directory: packageDir}
	proc, err := host.Open(t.Context(), installed, host.Options{
		Launch:  &process.Command{Path: binary, Args: []string{"-test.run=^TestEmailRuntimeGuestProcess$"}, Dir: packageDir, Env: []string{emailRuntimeGuestEnv + "=1"}},
		Reverse: reverse,
	})
	if err != nil {
		t.Fatalf("start fresh Email child: %v", err)
	}
	defer proc.Close()

	configuration := json.RawMessage(`{}`)
	var started runtimecontract.RuntimeStartResponse
	if err := proc.Call(t.Context(), plugin.Method(runtimecontract.RuntimeContract, runtimecontract.OperationStart), runtimecontract.RuntimeStartRequest{
		Configuration: configuration, MaxBodySize: 1 << 20, InstanceKey: []byte("fresh-email-test-key"),
	}, &started); err != nil {
		_ = proc.Close()
		t.Fatalf("start fresh Email runtime: %v (guest stderr: %s)", err, proc.Stderr())
	}
	if !slices.Contains(started.DeliveryAdapters, "mailbox.smtp") {
		t.Fatalf("fresh Email process did not advertise mailbox.smtp: %+v", started)
	}

	delivery := runtimecontract.GatewayDelivery{
		Protocol: runtimecontract.ProtocolSMTP, Adapter: "mailbox.smtp", Target: "recipient@example.test",
		Attributes: map[string]string{"mailbox_connection_id": mailboxID, "mail_from": "sender@gmail.com"},
		Payload:    []byte("From: sender@gmail.com\r\nTo: recipient@example.test\r\nSubject: OAuth child regression\r\n\r\nbody\r\n"),
	}
	request := runtimecontract.DeliveryAdapterRequest{Adapter: "mailbox.smtp", Delivery: delivery}
	var first runtimecontract.DeliveryAdapterResponse
	if err := proc.Call(t.Context(), plugin.Method(runtimecontract.DeliveryAdapterContract, runtimecontract.OperationDeliverAdapter), request, &first); err != nil {
		t.Fatalf("first mailbox.smtp call killed a fresh Email child: %v (guest stderr: %s)", err, proc.Stderr())
	}
	if first.Failure != runtimecontract.AdapterFailureTransient || passThroughCalls != 1 {
		t.Fatalf("first OAuth delivery response = %+v; credential lookups=%d", first, passThroughCalls)
	}
	var second runtimecontract.DeliveryAdapterResponse
	if err := proc.Call(t.Context(), plugin.Method(runtimecontract.DeliveryAdapterContract, runtimecontract.OperationDeliverAdapter), request, &second); err != nil {
		t.Fatalf("second mailbox.smtp call found a closed Email child: %v (guest stderr: %s)", err, proc.Stderr())
	}
	if second.Failure != runtimecontract.AdapterFailureTransient || passThroughCalls != 2 {
		t.Fatalf("second OAuth delivery response = %+v; credential lookups=%d", second, passThroughCalls)
	}
}

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

func TestEmailRuntimeAdvertisesNativeIMAPOnlyWhenMailboxPollingIsConfigured(t *testing.T) {
	response, err := new(runtime).start(context.Background(), runtimecontract.RuntimeStartRequest{
		Configuration: json.RawMessage(`{"mailbox_poll":{}}`),
		MaxBodySize:   1024 * 1024,
		InstanceKey:   []byte("extension-scoped-test-key"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Jobs) != 1 || response.Jobs[0] != (runtimecontract.RuntimeJob{Name: "imap", Language: "native.imap"}) {
		t.Fatalf("advertised native jobs = %+v", response.Jobs)
	}
}

func TestEmailRuntimeRejectsUnknownMailboxPollScheduleActions(t *testing.T) {
	runtime := new(runtime)
	_, err := runtime.start(context.Background(), runtimecontract.RuntimeStartRequest{
		Configuration: json.RawMessage(`{"mailbox_poll":{}}`), MaxBodySize: 1024 * 1024,
		InstanceKey: []byte("extension-scoped-test-key"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(runtimecontract.JobRequest{Name: "imap", Source: "poll-1", Payload: map[string]any{"action": "unexpected"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handle(context.Background(), plugin.Request{
		Contract: runtimecontract.JobContract, Operation: runtimecontract.OperationRunJob, Payload: payload,
	}); err == nil {
		t.Fatal("Email runtime accepted an unsupported mailbox polling action")
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
