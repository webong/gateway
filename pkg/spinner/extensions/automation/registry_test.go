//go:build !gateway_javascript && !gateway_lua

package automationextensions

import (
	"context"
	"testing"

	"github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/gateway/driver/automation"
)

func TestDefaultCompositionHasNoBundledLanguages(t *testing.T) {
	runtime := Runtime()
	if runtime.Supports("javascript") || runtime.Supports("lua") {
		t.Fatal("default Gateway build unexpectedly includes a script runtime")
	}
}

type lateEngine struct{ language automation.Language }

func (e lateEngine) Language() automation.Language { return e.language }
func (e lateEngine) Execute(context.Context, string, automation.Input) (automation.Outcome, error) {
	return automation.Outcome{Response: &automation.Response{Status: 204}}, nil
}

func TestRuntimeObservesGuestsInstalledAfterSpinnerStarted(t *testing.T) {
	plugins := NewRegistry()
	runtime := plugins.Runtime()
	language := automation.Language("post-deploy")
	if runtime.Supports(language) {
		t.Fatal("runtime advertised a guest before it was installed")
	}
	engine := lateEngine{language: language}
	plugins.Register(NewEngineAdapter(engine, EngineDescriptor(engine, contract.Identity{
		ID: "test/post-deploy", Revision: "r1", Version: "1.0.0",
	})))
	if !runtime.Supports(language) {
		t.Fatal("running Spinner did not observe the newly installed guest")
	}
	result, err := runtime.Execute(t.Context(), automation.Script{Language: language, Source: "ignored"}, automation.Input{})
	if err != nil || result.Response == nil || result.Response.Status != 204 {
		t.Fatalf("newly installed guest did not execute: %+v, %v", result, err)
	}
}
