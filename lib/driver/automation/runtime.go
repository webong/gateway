// Package automation defines the shared interface between Gateway hosts and
// optional automation language extensions. It owns language dispatch and
// outcome validation without depending on Spinner or any interpreter.
package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const MaxDeliveries = 32

var ErrUnsupportedLanguage = errors.New("unsupported automation language")

type Language string

type Script struct {
	Language Language
	Source   string
}

type Input struct {
	Event     map[string]any
	Variables map[string]any
}

type Delivery struct {
	URL     string              `json:"url"`
	Method  string              `json:"method,omitempty"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
}

type Response struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    string              `json:"body,omitempty"`
}

type Outcome struct {
	Variables  map[string]any
	Deliveries []Delivery
	Response   *Response
}

// Engine is implemented by one optional language package. Engines receive no
// delivery client; they can only return declarative deliveries to the host.
type Engine interface {
	Language() Language
	Execute(context.Context, string, Input) (Outcome, error)
}

// Executor is the narrow capability consumed by ingress and schedule paths.
type Executor interface {
	Execute(context.Context, Script, Input) (Outcome, error)
	Supports(Language) bool
}

// LanguageCatalog is implemented by executors that can report the engines
// present in the running host. Hosts advertise this list to Planner so it does
// not confuse an installed PHP companion with an available Go runtime.
type LanguageCatalog interface {
	Languages() []Language
}

// Runtime dispatches scripts to explicitly installed language engines.
// A zero-value Runtime supports no languages and therefore fails closed.
type Runtime struct {
	engines map[Language]Engine
}

func NewRuntime(engines ...Engine) *Runtime {
	runtime := &Runtime{engines: make(map[Language]Engine, len(engines))}
	for _, engine := range engines {
		if engine == nil || strings.TrimSpace(string(engine.Language())) == "" {
			continue
		}
		runtime.engines[engine.Language()] = engine
	}
	return runtime
}

func (r *Runtime) Supports(language Language) bool {
	if r == nil {
		return false
	}
	_, ok := r.engines[language]
	return ok
}

func (r *Runtime) Languages() []Language {
	if r == nil {
		return []Language{}
	}
	languages := make([]Language, 0, len(r.engines))
	for language := range r.engines {
		languages = append(languages, language)
	}
	sort.Slice(languages, func(i, j int) bool { return languages[i] < languages[j] })
	return languages
}

// Languages returns a defensive copy of the engines advertised by an
// executor. Custom executors that do not implement LanguageCatalog advertise
// no languages and therefore fail closed in Planner.
func Languages(executor Executor) []Language {
	catalog, ok := executor.(LanguageCatalog)
	if !ok || catalog == nil {
		return []Language{}
	}
	return append([]Language(nil), catalog.Languages()...)
}

func (r *Runtime) Execute(ctx context.Context, script Script, input Input) (Outcome, error) {
	if strings.TrimSpace(script.Source) == "" {
		return Outcome{}, fmt.Errorf("automation source is required")
	}
	if r == nil {
		return Outcome{}, fmt.Errorf("%w %q", ErrUnsupportedLanguage, script.Language)
	}
	engine, ok := r.engines[script.Language]
	if !ok {
		return Outcome{}, fmt.Errorf("%w %q", ErrUnsupportedLanguage, script.Language)
	}

	input.Variables = CloneVariables(input.Variables)
	outcome, err := engine.Execute(ctx, script.Source, input)
	if err != nil {
		return Outcome{}, err
	}
	if outcome.Variables == nil {
		outcome.Variables = input.Variables
	}
	if err := validateOutcome(outcome); err != nil {
		return Outcome{}, fmt.Errorf("%s automation: %w", script.Language, err)
	}
	return outcome, nil
}

func CloneVariables(values map[string]any) map[string]any {
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func validateOutcome(outcome Outcome) error {
	if len(outcome.Deliveries) > MaxDeliveries {
		return fmt.Errorf("exceeded the delivery limit")
	}
	if outcome.Response != nil && (outcome.Response.Status < 100 || outcome.Response.Status > 599) {
		return fmt.Errorf("returned an invalid response status")
	}
	for _, delivery := range outcome.Deliveries {
		if strings.TrimSpace(delivery.URL) == "" {
			return fmt.Errorf("returned a delivery without a URL")
		}
	}
	return nil
}

var _ Executor = (*Runtime)(nil)
var _ LanguageCatalog = (*Runtime)(nil)
