package workload

import (
	"context"
	"testing"

	gatewayautomation "github.com/webong/gateway/driver/automation"
)

func TestAutomationExtensionUsesPublishedDriverContracts(t *testing.T) {
	runtime := gatewayautomation.NewRuntime(AutomationEngine{})
	languages := gatewayautomation.Languages(runtime)
	if len(languages) != 1 || languages[0] != "example" {
		t.Fatalf("unexpected advertised languages: %v", languages)
	}
	result, err := runtime.Execute(context.Background(), gatewayautomation.Script{
		Language: "example",
		Source:   "run",
	}, gatewayautomation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Response == nil || result.Response.Status != 202 || result.Response.Body != "extension" {
		t.Fatalf("unexpected automation result: %+v", result)
	}
}
