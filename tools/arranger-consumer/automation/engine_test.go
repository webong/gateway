package automation

import (
	"context"
	"testing"

	arrangerautomation "github.com/webong/gateway/arranger/automation"
)

func TestEngineUsesPublishedArrangerContracts(t *testing.T) {
	runtime := arrangerautomation.NewRuntime(Engine{})
	languages := arrangerautomation.Languages(runtime)
	if len(languages) != 1 || languages[0] != "example" {
		t.Fatalf("unexpected advertised languages: %v", languages)
	}
	result, err := runtime.Execute(context.Background(), arrangerautomation.Script{
		Language: "example",
		Source:   "run",
	}, arrangerautomation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Response == nil || result.Response.Status != 202 || result.Response.Body != "extension" {
		t.Fatalf("unexpected automation result: %+v", result)
	}
}
