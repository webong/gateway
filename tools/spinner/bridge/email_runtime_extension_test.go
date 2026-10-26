package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	smtpclient "github.com/emersion/go-smtp"
	mdns "github.com/miekg/dns"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
	protocol "github.com/webong/gateway/arranger/integration"
	integrationextensions "github.com/webong/gateway/pkg/spinner/extensions/integration"
)

type runtimeEmailExecutor struct {
	deliveries chan protocol.GatewayDelivery
}

func (executor *runtimeEmailExecutor) Deliver(context.Context, protocol.Delivery) (protocol.Response, error) {
	return protocol.Response{}, nil
}

func (executor *runtimeEmailExecutor) Enqueue(protocol.Delivery) bool { return false }

func (executor *runtimeEmailExecutor) EnqueueGateway(delivery protocol.GatewayDelivery) bool {
	executor.deliveries <- delivery
	return true
}

func (executor *runtimeEmailExecutor) DurableGatewayDeliveries() bool { return true }

func TestEmailCanBeInstalledWithDNSAfterSpinnerStarts(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	storeRoot := t.TempDir()
	for _, extension := range []string{"dns", "email"} {
		destination := filepath.Join(storeRoot, "webong%2f"+extension)
		command := exec.Command(
			filepath.Join(root, "scripts", "build-gateway-runtime-extension"),
			filepath.Join(root, "pkg", extension), destination, "0.1.0", "test-"+extension+"-runtime-r1",
		)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %s runtime extension: %v\n%s", extension, err, output)
		}
	}

	packages, err := store.New(storeRoot, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed := make(map[string]store.Installed, 2)
	manifests := make([]packagekit.Manifest, 0, 2)
	for _, identity := range []string{"webong/email", "webong/dns"} {
		loaded, err := packages.Load(identity)
		if err != nil {
			t.Fatal(err)
		}
		installed[identity] = loaded
		manifests = append(manifests, loaded.Manifest)
	}
	plan, err := packagekit.Resolve(manifests)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Order) != 2 || plan.Order[0].ID != "webong/dns" || plan.Order[1].ID != "webong/email" {
		t.Fatalf("unexpected Email dependency order: %+v", plan.Order)
	}

	dnsAddress, dnsCalls, stopDNS := startRuntimeDNSUpstream(t)
	t.Cleanup(stopDNS)
	smtpAddress := availableTCPAddress(t)
	configurations := map[string]json.RawMessage{
		"webong/dns": mustJSON(t, map[string]any{
			"resolver_address": dnsAddress, "resolver_timeout_ms": 1000,
		}),
		"webong/email": mustJSON(t, map[string]any{
			"smtp": map[string]any{
				"address": smtpAddress, "hostname": "smtp.example.test", "dns_lookup_timeout_ms": 1000,
			},
		}),
	}
	executor := &runtimeEmailExecutor{deliveries: make(chan protocol.GatewayDelivery, 1)}
	submissions := make(chan string, 1)
	integrationextensions.DefaultHost.Configure(integrationextensions.Services{
		ProtocolPlanner: runtimeProtocolPlanner(func(_ context.Context, event protocol.GatewayEvent) (protocol.GatewayDecision, error) {
			if event.Protocol != protocol.ProtocolSMTP || event.Kind != protocol.EventTransaction {
				return protocol.GatewayDecision{}, fmt.Errorf("unexpected Email event: %+v", event)
			}
			return protocol.GatewayDecision{
				Version: protocol.ProtocolVersion, Protocol: protocol.ProtocolSMTP, Action: protocol.GatewaySubmit,
				Deliveries: []protocol.GatewayDelivery{{
					Protocol: protocol.ProtocolSMTP, Adapter: "managed.smtp", Target: "inbox@example.test",
				}},
			}, nil
		}),
		Executor: executor,
		PassThrough: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			body := new(bytes.Buffer)
			_, _ = body.ReadFrom(request.Body)
			submissions <- body.String()
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte("accepted by Planner"))
		}),
	})
	registry := integrationextensions.NewRegistry()
	for _, identity := range plan.Order {
		bindings := make([]packagekit.Resolution, 0, len(plan.Bindings))
		for _, binding := range plan.Bindings {
			if binding.Consumer == identity {
				bindings = append(bindings, binding)
			}
		}
		if err := integrationextensions.LoadExternalInstalledWithDependencies(
			t.Context(), registry, installed[identity.ID], configurations[identity.ID], bindings,
		); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		registry.Unregister("webong/email")
		registry.Unregister("webong/dns")
	})

	submission := httptest.NewRequest(http.MethodPost, "http://gateway.test/email/submissions", strings.NewReader(`{"message":"hello"}`))
	submissionResponse := httptest.NewRecorder()
	registry.Handler().ServeHTTP(submissionResponse, submission)
	if submissionResponse.Code != http.StatusAccepted || submissionResponse.Body.String() != "accepted by Planner" {
		t.Fatalf("packaged Email HTTP mount returned %d %q", submissionResponse.Code, submissionResponse.Body.String())
	}
	select {
	case body := <-submissions:
		if body != `{"message":"hello"}` {
			t.Fatalf("unexpected Email submission body: %q", body)
		}
	case <-time.After(time.Second):
		t.Fatal("packaged Email HTTP mount did not pass through to Planner")
	}

	client, err := smtpclient.Dial(smtpAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	message := []byte("From: sender@example.test\r\nTo: inbox@example.test\r\nSubject: packaged Email smoke\r\n\r\nbody\r\n")
	if err := client.SendMail("sender@example.test", []string{"inbox@example.test"}, bytes.NewReader(message)); err != nil {
		t.Fatal(err)
	}
	select {
	case delivery := <-executor.deliveries:
		if delivery.Adapter != "managed.smtp" || delivery.Target != "inbox@example.test" ||
			!bytes.Contains(delivery.Payload, []byte("Subject: packaged Email smoke")) {
			t.Fatalf("unexpected packaged Email delivery: %+v", delivery)
		}
	case <-time.After(time.Second):
		t.Fatal("packaged Email runtime did not enqueue its durable delivery")
	}
	if calls := dnsCalls(); calls == 0 {
		t.Fatal("packaged Email runtime did not use its resolved DNS dependency")
	}
}

func startRuntimeDNSUpstream(t *testing.T) (string, func() int, func()) {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := 0
	server := &mdns.Server{PacketConn: packet, Handler: mdns.HandlerFunc(func(writer mdns.ResponseWriter, request *mdns.Msg) {
		mu.Lock()
		calls++
		mu.Unlock()
		response := new(mdns.Msg)
		response.SetReply(request)
		response.RecursionAvailable = true
		if len(request.Question) == 1 && request.Question[0].Qtype == mdns.TypeA {
			response.Answer = append(response.Answer, &mdns.A{
				Hdr: mdns.RR_Header{Name: request.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 60},
				A:   net.ParseIP("127.0.0.1").To4(),
			})
		}
		_ = writer.WriteMsg(response)
	})}
	go func() { _ = server.ActivateAndServe() }()
	return packet.LocalAddr().String(), func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}, func() { _ = server.Shutdown() }
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
