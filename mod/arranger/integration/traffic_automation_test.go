package integration_test

import (
	"testing"

	"github.com/webong/gateway/arranger/automation"
	"github.com/webong/gateway/arranger/integration"
)

func TestGatewayAutomateDecisionRequiresOneValidAutomation(t *testing.T) {
	valid := integration.GatewayDecision{
		Version: integration.ProtocolVersion, Protocol: integration.ProtocolDNS,
		Action:     integration.GatewayAutomate,
		Automation: &integration.Automation{Language: "javascript", Source: "gateway.respond('[]', 200)"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid automate decision rejected: %v", err)
	}

	for name, decision := range map[string]integration.GatewayDecision{
		"missing automation": {
			Version: integration.ProtocolVersion, Protocol: integration.ProtocolDNS, Action: integration.GatewayAutomate,
		},
		"unknown executor": {
			Version: integration.ProtocolVersion, Protocol: integration.ProtocolDNS, Action: integration.GatewayAutomate,
			Automation: &integration.Automation{Language: "javascript", Source: "run()", Executor: automation.ExecutorMode("process")},
		},
		"automation on another action": {
			Version: integration.ProtocolVersion, Protocol: integration.ProtocolDNS, Action: integration.GatewayRespond,
			Automation: &integration.Automation{Language: "javascript", Source: "run()"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := decision.Validate(); err == nil {
				t.Fatal("invalid automate decision was accepted")
			}
		})
	}
}
