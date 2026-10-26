package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	smtpclient "github.com/emersion/go-smtp"
	smtpserver "github.com/emersion/go-smtp"
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

func TestFreshEmailProcessCompletesOAuthMailboxSMTPDelivery(t *testing.T) {
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

	serverCertificate, rootCertificate := oauthSMTPTestCertificate(t)
	rootPath := filepath.Join(t.TempDir(), "oauth-smtp-root.pem")
	if err := os.WriteFile(rootPath, rootCertificate, 0600); err != nil {
		t.Fatal(err)
	}
	privateIP := privateTestIPv4(t)
	listener, err := net.Listen("tcp", net.JoinHostPort(privateIP.String(), "0"))
	if err != nil {
		t.Fatalf("listen on private test address %s: %v", privateIP, err)
	}
	backend := &oauthSMTPBackend{}
	server := smtpserver.NewServer(backend)
	var smtpTrace bytes.Buffer
	server.Debug = &smtpTrace
	server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{serverCertificate}, MinVersion: tls.VersionTLS12}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-serveErr
	})

	mailboxID := "11111111-1111-4111-8111-111111111111"
	credential := smtpout.MailboxCredential{
		UpstreamID: "gmail", Username: "sender@gmail.com", Secret: "short-lived-test-token",
		Mechanism: "XOAUTH2", Address: "sender@gmail.com", UpstreamAddress: listener.Addr().String(),
		UpstreamServerName: "smtp.test", UpstreamTLSMode: "starttls", UpstreamNetworkScope: "private",
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
			if len(input.Body) != 0 {
				return nil, fmt.Errorf("bodyless mailbox credential lookup carried %d bytes", len(input.Body))
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
		Launch: &process.Command{
			Path: binary,
			Args: []string{"-test.run=^TestEmailRuntimeGuestProcess$"},
			Dir:  packageDir,
			Env:  []string{emailRuntimeGuestEnv + "=1", "SSL_CERT_FILE=" + rootPath, "GODEBUG=x509sslcertoverrideplatform=1"},
		},
		Reverse: reverse,
	})
	if err != nil {
		t.Fatalf("start fresh Email child: %v", err)
	}
	defer proc.Close()

	var started runtimecontract.RuntimeStartResponse
	if err := proc.Call(t.Context(), plugin.Method(runtimecontract.RuntimeContract, runtimecontract.OperationStart), runtimecontract.RuntimeStartRequest{
		Configuration: json.RawMessage("{}"), MaxBodySize: 1 << 20, InstanceKey: []byte("fresh-email-test-key"),
	}, &started); err != nil {
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
	for attempt := 1; attempt <= 2; attempt++ {
		var response runtimecontract.DeliveryAdapterResponse
		request := runtimecontract.DeliveryAdapterRequest{Adapter: "mailbox.smtp", Delivery: delivery}
		if err := proc.Call(t.Context(), plugin.Method(runtimecontract.DeliveryAdapterContract, runtimecontract.OperationDeliverAdapter), request, &response); err != nil {
			t.Fatalf("OAuth SMTP attempt %d killed the fresh Email child: %v (guest stderr: %s)", attempt, err, proc.Stderr())
		}
		if response.Failure != "" {
			trace := strings.Split(smtpTrace.String(), "\n")
			for index, line := range trace {
				if strings.Contains(strings.ToUpper(line), "AUTH") {
					trace[index] = "[SMTP authentication line redacted]"
				}
			}
			t.Fatalf("OAuth SMTP attempt %d failed: %+v; auths=%d; SMTP trace:\n%s", attempt, response, backend.auths, strings.Join(trace, "\n"))
		}
	}
	if passThroughCalls != 2 {
		t.Fatalf("Planner credential lookups = %d, want 2", passThroughCalls)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.auths != 2 || len(backend.messages) != 2 {
		t.Fatalf("SMTP OAuth authentications=%d accepted messages=%d, want 2 each", backend.auths, len(backend.messages))
	}
	for index, message := range backend.messages {
		if !bytes.Contains(message, []byte("Subject: OAuth child regression")) {
			t.Fatalf("SMTP delivery %d did not contain the original message: %q", index+1, message)
		}
	}
}

type oauthSMTPBackend struct {
	mu       sync.Mutex
	auths    int
	messages [][]byte
}

func (backend *oauthSMTPBackend) NewSession(*smtpserver.Conn) (smtpserver.Session, error) {
	return &oauthSMTPSession{backend: backend}, nil
}

type oauthSMTPSession struct{ backend *oauthSMTPBackend }

func (*oauthSMTPSession) AuthMechanisms() []string { return []string{"XOAUTH2"} }
func (session *oauthSMTPSession) Auth(mechanism string) (sasl.Server, error) {
	if mechanism != "XOAUTH2" {
		return nil, smtpserver.ErrAuthUnsupported
	}
	return oauthSMTPAuth{backend: session.backend}, nil
}
func (*oauthSMTPSession) Mail(string, *smtpserver.MailOptions) error { return nil }
func (*oauthSMTPSession) Rcpt(string, *smtpserver.RcptOptions) error { return nil }
func (session *oauthSMTPSession) Data(reader io.Reader) error {
	message, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	session.backend.mu.Lock()
	session.backend.messages = append(session.backend.messages, message)
	session.backend.mu.Unlock()
	return nil
}
func (*oauthSMTPSession) Reset()        {}
func (*oauthSMTPSession) Logout() error { return nil }

type oauthSMTPAuth struct{ backend *oauthSMTPBackend }

func (auth oauthSMTPAuth) Next(response []byte) ([]byte, bool, error) {
	want := []byte("user=sender@gmail.com\x01auth=Bearer short-lived-test-token\x01\x01")
	if !bytes.Equal(response, want) {
		return nil, false, errors.New("OAuth SMTP client sent unexpected XOAUTH2 credentials")
	}
	auth.backend.mu.Lock()
	auth.backend.auths++
	auth.backend.mu.Unlock()
	return nil, true, nil
}

func oauthSMTPTestCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "smtp.test"}, DNSNames: []string{"smtp.test"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey})
	certificate, err := tls.X509KeyPair(certificatePEM, privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, certificatePEM
}

func privateTestIPv4(t *testing.T) net.IP {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, err := networkInterface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			var ip net.IP
			switch parsed := address.(type) {
			case *net.IPNet:
				ip = parsed.IP
			case *net.IPAddr:
				ip = parsed.IP
			}
			if ip != nil && ip.To4() != nil && ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				return ip.To4()
			}
		}
	}
	t.Skip("OAuth SMTP child test requires a private non-loopback IPv4 interface")
	return nil
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
