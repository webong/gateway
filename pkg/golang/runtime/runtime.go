// Package runtime implements the optional Yaegi (Go interpreter) automation extension.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
	"github.com/webong/gateway/arranger/automation"
)

const Language automation.Language = "go"

type Engine struct {
	Timeout       time.Duration
	allowedPkgs   []string
	interpreter   *interp.Interpreter
	initOnce      bool
}

func New(opts ...Option) *Engine {
	e := &Engine{
		Timeout:     50 * time.Millisecond,
		allowedPkgs: defaultAllowedPackages(),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func (e *Engine) Language() automation.Language { return Language }

type Option func(*Engine)

func WithTimeout(d time.Duration) Option {
	return func(e *Engine) { e.Timeout = d }
}

func WithAllowedPackages(pkgs []string) Option {
	return func(e *Engine) { e.allowedPkgs = pkgs }
}

func defaultAllowedPackages() []string {
	return []string{
		"fmt",
		"strings",
		"encoding/json",
		"time",
		"math",
		"sort",
		"bytes",
		"hash",
		"hash/sha256",
		"encoding/hex",
		"encoding/base64",
	}
}

func (e *Engine) ensureInterpreter() error {
	if e.initOnce {
		return nil
	}
	i := interp.New(interp.Options{})

	symbols := make(map[string]map[string]reflect.Value)
	for _, pkg := range e.allowedPkgs {
		if stdlibPkg, ok := stdlib.Symbols[pkg]; ok {
			symbols[pkg] = stdlibPkg
		}
	}

	// Expose the gateway automation API types
	symbols["github.com/webong/gateway/arranger/automation"] = map[string]reflect.Value{
		"Language":  reflect.ValueOf(automation.Language("")),
		"Script":    reflect.ValueOf(automation.Script{}),
		"Input":     reflect.ValueOf(automation.Input{}),
		"Delivery":  reflect.ValueOf(automation.Delivery{}),
		"Response":  reflect.ValueOf(automation.Response{}),
		"Schedule":  reflect.ValueOf(automation.Schedule{}),
		"Outcome":   reflect.ValueOf(automation.Outcome{}),
	}

	if err := i.Use(symbols); err != nil {
		return fmt.Errorf("load Yaegi symbols: %w", err)
	}

	if _, err := i.Eval(`import "github.com/webong/gateway/arranger/automation"`); err != nil {
		return fmt.Errorf("pre-import automation: %w", err)
	}

	e.interpreter = i
	e.initOnce = true
	return nil
}

func (e *Engine) Execute(ctx context.Context, source string, input automation.Input) (automation.Outcome, error) {
	if err := e.ensureInterpreter(); err != nil {
		return automation.Outcome{}, err
	}

	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build the script that uses the gateway API
	script := buildScript(source)

	v, err := e.interpreter.EvalWithContext(ctx, script)
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("evaluate Go automation: %w", err)
	}

	// The script returns a JSON string
	var outcome automation.Outcome
	if err := json.Unmarshal([]byte(v.String()), &outcome); err != nil {
		return automation.Outcome{}, fmt.Errorf("decode Yaegi automation outcome: %w", err)
	}
	return outcome, nil
}

func buildScript(source string) string {
	// Escape source for Go string literal
	escaped := strings.ReplaceAll(source, "`", "` + \"`\" + `")
	return `package main

import (
	"encoding/json"
	"strings"
	"github.com/webong/gateway/arranger/automation"
)

func run(inputJSON string) string {
	var input automation.Input
	json.Unmarshal([]byte(inputJSON), &input)

	state := automation.Outcome{
		Variables:  input.Variables,
		Deliveries: []automation.Delivery{},
		Schedules:  []automation.Schedule{},
	}

	gateway := struct {
		Get     func(string) any
		Set     func(string, any)
		Respond func(string, int)
		Forward func(automation.Delivery)
		Schedule func(automation.Schedule)
	}{
		Get: func(path string) any {
			parts := strings.Split(path, ".")
			var current any = state.Variables
			for _, part := range parts {
				m, ok := current.(map[string]any)
				if !ok { return nil }
				current, ok = m[part]
				if !ok { return nil }
			}
			return current
		},
		Set: func(path string, value any) {
			parts := strings.Split(path, ".")
			var current map[string]any = state.Variables
			for i, part := range parts[:len(parts)-1] {
				next, ok := current[part].(map[string]any)
				if !ok {
					next = make(map[string]any)
					current[part] = next
				}
				current = next
			}
			current[parts[len(parts)-1]] = value
		},
		Respond: func(body string, status int) {
			state.Response = &automation.Response{Status: status, Body: body}
		},
		Forward: func(d automation.Delivery) {
			state.Deliveries = append(state.Deliveries, d)
		},
		Schedule: func(s automation.Schedule) {
			state.Schedules = append(state.Schedules, s)
		},
	}

` + escaped + `

	out, _ := json.Marshal(state)
	return string(out)
}
`
}

var _ automation.Engine = (*Engine)(nil)

// Plugin returns the engine for composition roots and dynamic loaders.
func Plugin() (automation.Engine, error) { return New(), nil }
