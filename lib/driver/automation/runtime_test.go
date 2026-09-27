package automation

import (
	"context"
	"errors"
	"testing"
)

type fakeEngine struct {
	language Language
	outcome  Outcome
}

func (e fakeEngine) Language() Language { return e.language }
func (e fakeEngine) Execute(context.Context, string, Input) (Outcome, error) {
	return e.outcome, nil
}

func TestRuntimeOnlyDispatchesToInstalledEngines(t *testing.T) {
	runtime := NewRuntime(
		fakeEngine{language: "second", outcome: Outcome{}},
		fakeEngine{language: "example", outcome: Outcome{Response: &Response{Status: 204}}},
	)
	if !runtime.Supports("example") || runtime.Supports("javascript") {
		t.Fatal("runtime reported the wrong installed languages")
	}
	languages := Languages(runtime)
	if len(languages) != 2 || languages[0] != "example" || languages[1] != "second" {
		t.Fatalf("runtime returned an unstable language catalog: %v", languages)
	}
	result, err := runtime.Execute(context.Background(), Script{Language: "example", Source: "run"}, Input{})
	if err != nil || result.Response == nil || result.Response.Status != 204 {
		t.Fatalf("unexpected result: %#v, %v", result, err)
	}
	_, err = runtime.Execute(context.Background(), Script{Language: "javascript", Source: "run"}, Input{})
	if !errors.Is(err, ErrUnsupportedLanguage) {
		t.Fatalf("expected unsupported language error, got %v", err)
	}
}

func TestRuntimeValidatesExtensionOutput(t *testing.T) {
	runtime := NewRuntime(fakeEngine{language: "example", outcome: Outcome{
		Deliveries: []Delivery{{URL: ""}},
	}})
	if _, err := runtime.Execute(context.Background(), Script{Language: "example", Source: "run"}, Input{}); err == nil {
		t.Fatal("expected invalid extension output to be rejected")
	}
}

func TestRuntimeValidatesDeclarativeSchedules(t *testing.T) {
	runtime := NewRuntime(fakeEngine{language: "example", outcome: Outcome{
		Schedules: []Schedule{{
			Key: "daily-report", Name: "Daily report", Cron: "0 9 * * *",
			Language: "example", Source: "run()",
		}},
	}})
	result, err := runtime.Execute(context.Background(), Script{Language: "example", Source: "run"}, Input{})
	if err != nil || len(result.Schedules) != 1 {
		t.Fatalf("expected a valid schedule declaration, got %#v, %v", result, err)
	}

	runtime = NewRuntime(fakeEngine{language: "example", outcome: Outcome{
		Schedules: []Schedule{
			{Key: "duplicate", Name: "One", Cron: "* * * * *", Language: "example", Source: "run()"},
			{Key: "duplicate", Name: "Two", Cron: "* * * * *", Language: "example", Source: "run()"},
		},
	}})
	if _, err := runtime.Execute(context.Background(), Script{Language: "example", Source: "run"}, Input{}); err == nil {
		t.Fatal("expected duplicate schedule keys to fail validation")
	}
}
