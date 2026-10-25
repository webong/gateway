package gateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	"github.com/webong/gateway/arranger/automation"
	protocol "github.com/webong/gateway/arranger/integration"
	integrationextensions "github.com/webong/gateway/pkg/spinner/extensions/integration"
)

type runtimeProtocolPlanner func(context.Context, protocol.GatewayEvent) (protocol.GatewayDecision, error)

func (f runtimeProtocolPlanner) PlanEvent(ctx context.Context, event protocol.GatewayEvent) (protocol.GatewayDecision, error) {
	return f(ctx, event)
}

type runtimeObservationSink func(context.Context, protocol.Observation) error

func (f runtimeObservationSink) RecordObservation(ctx context.Context, observation protocol.Observation) error {
	return f(ctx, observation)
}

type runtimeDNSAutomation struct {
	inputs chan automation.Input
}

func (runtime *runtimeDNSAutomation) Supports(language automation.Language) bool {
	return language == "javascript"
}

func (runtime *runtimeDNSAutomation) Languages() []automation.Language {
	return []automation.Language{"javascript"}
}

func (runtime *runtimeDNSAutomation) Execute(_ context.Context, script automation.Script, input automation.Input) (automation.Outcome, error) {
	if script.Language != "javascript" || script.Source != "answer_dns()" {
		return automation.Outcome{}, fmt.Errorf("unexpected DNS automation script: %+v", script)
	}
	runtime.inputs <- input
	return automation.Outcome{Response: &automation.Response{
		Status: 200, Body: `[{"type":"TXT","value":"installed-automation"}]`,
	}}, nil
}

func TestDNSCanBeInstalledAfterSpinnerStarts(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	storeRoot := t.TempDir()
	destination := filepath.Join(storeRoot, "webong%2fdns")
	command := exec.Command(filepath.Join(root, "scripts", "build-gateway-runtime-extension"), filepath.Join(root, "pkg", "dns"), destination, "0.1.0", "test-dns-runtime-r1")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build DNS runtime extension: %v\n%s", err, output)
	}

	packages, err := store.New(storeRoot, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	installed, err := packages.Load("webong/dns")
	if err != nil {
		t.Fatal(err)
	}

	address := availableTCPAddress(t)
	configuration, err := json.Marshal(map[string]any{
		"address": address,
		"zones": []map[string]any{{
			"name": "dns.example.test", "nameservers": []string{"ns1.dns.example.test"},
		}},
		"planner_timeout_ms": 1000,
	})
	if err != nil {
		t.Fatal(err)
	}

	observations := make(chan protocol.Observation, 1)
	automations := &runtimeDNSAutomation{inputs: make(chan automation.Input, 1)}
	integrationextensions.DefaultHost.Configure(integrationextensions.Services{
		ProtocolPlanner: runtimeProtocolPlanner(func(_ context.Context, event protocol.GatewayEvent) (protocol.GatewayDecision, error) {
			if event.Protocol != protocol.ProtocolDNS || event.Route != "/hook" || event.Attributes["data"] != "value" {
				return protocol.GatewayDecision{}, fmt.Errorf("unexpected DNS event: %+v", event)
			}
			if len(event.Runtime.AutomationLanguages) != 1 || event.Runtime.AutomationLanguages[0] != "javascript" {
				return protocol.GatewayDecision{}, fmt.Errorf("DNS runtime omitted automation capability: %+v", event.Runtime)
			}
			return protocol.GatewayDecision{
				Version: protocol.ProtocolVersion, Protocol: protocol.ProtocolDNS, Action: protocol.GatewayAutomate,
				Automation: &protocol.Automation{Language: "javascript", Source: "answer_dns()"},
			}, nil
		}),
		Executor:   runtimeExecutor{},
		Automation: automations,
		Observations: runtimeObservationSink(func(_ context.Context, observation protocol.Observation) error {
			observations <- observation
			return nil
		}),
		Start: protocol.RuntimeStartRequest{AutomationLanguages: []automation.Language{"javascript"}},
	})
	registry := integrationextensions.NewRegistry()
	if err := integrationextensions.LoadExternalInstalledWithConfiguration(t.Context(), registry, installed, configuration); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Unregister("webong/dns") })

	message := new(mdns.Msg)
	message.SetQuestion("value.hook.dns.example.test.", mdns.TypeTXT)
	client := &mdns.Client{Timeout: time.Second}
	var response *mdns.Msg
	for attempt := 0; attempt < 20; attempt++ {
		response, _, err = client.Exchange(message, address)
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("query installed DNS runtime: %v", err)
	}
	answer, ok := firstTXT(response.Answer)
	if response.Rcode != mdns.RcodeSuccess || !ok || len(answer.Txt) != 1 || answer.Txt[0] != "installed-automation" {
		t.Fatalf("unexpected installed DNS answer: %+v", response)
	}
	select {
	case input := <-automations.inputs:
		query, ok := input.Variables["dns"].(map[string]any)
		if !ok || query["data"] != "value" {
			t.Fatalf("installed DNS runtime sent unexpected automation input: %+v", input)
		}
	case <-time.After(time.Second):
		t.Fatal("installed DNS runtime did not invoke its automation host")
	}
	select {
	case observation := <-observations:
		if observation.Type != "dns.query" || observation.Attributes["mode"] != "provided" ||
			observation.Attributes["qname"] != "value.hook.dns.example.test." ||
			observation.Attributes["response_name"] != "NOERROR" {
			t.Fatalf("unexpected installed DNS observation: %+v", observation)
		}
	case <-time.After(time.Second):
		t.Fatal("installed DNS runtime did not emit its post-answer observation")
	}
}

func firstTXT(answers []mdns.RR) (*mdns.TXT, bool) {
	if len(answers) != 1 {
		return nil, false
	}
	answer, ok := answers[0].(*mdns.TXT)
	return answer, ok
}

func availableTCPAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
