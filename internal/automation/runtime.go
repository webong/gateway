// Package automation executes Gateway's small, bounded ingress scripts.
//
// Scripts do not receive filesystem, process, database, package-import, or
// raw network capabilities. Generated HTTP deliveries are returned to the
// Gateway edge, which applies its normal outbound policy before executing
// them.
package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/webong/gateway/internal/automation/qjs"
	lua "github.com/yuin/gopher-lua"
)

type Language string

const (
	LanguageWebhookScript Language = "webhookscript"
	LanguageLua           Language = "lua"
	LanguageJavaScript    Language = "javascript"
)

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

// Runtime limits are deliberately router-owned, rather than being supplied by
// a planner or a user-authored script.
type Runtime struct {
	Timeout     time.Duration
	MemoryLimit uint64
}

func (r Runtime) Execute(ctx context.Context, script Script, input Input) (Outcome, error) {
	if strings.TrimSpace(script.Source) == "" {
		return Outcome{}, fmt.Errorf("automation source is required")
	}
	if r.Timeout <= 0 {
		r.Timeout = 50 * time.Millisecond
	}
	if r.MemoryLimit == 0 {
		r.MemoryLimit = 4 * 1024 * 1024
	}

	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	outcome := Outcome{Variables: cloneMap(input.Variables)}
	switch script.Language {
	case LanguageJavaScript:
		return outcome, r.executeJavaScript(ctx, script.Source, input, &outcome)
	case LanguageWebhookScript:
		return outcome, r.executeJavaScript(ctx, webhookScriptToJavaScript(script.Source), input, &outcome)
	case LanguageLua:
		return outcome, r.executeLua(ctx, script.Source, input, &outcome)
	default:
		return Outcome{}, fmt.Errorf("unsupported automation language %q", script.Language)
	}
}

func (r Runtime) executeJavaScript(ctx context.Context, source string, input Input, outcome *Outcome) error {
	inputJSON, err := json.Marshal(map[string]any{
		"event":     input.Event,
		"variables": outcome.Variables,
	})
	if err != nil {
		return fmt.Errorf("marshal automation event: %w", err)
	}
	outputJSON, err := qjs.Run(ctx, javascriptPrelude+source+javascriptSuffix, inputJSON, r.MemoryLimit, r.Timeout)
	if err != nil {
		return fmt.Errorf("evaluate javascript automation: %w", err)
	}
	if err := json.Unmarshal(outputJSON, outcome); err != nil {
		return fmt.Errorf("decode javascript automation outcome: %w", err)
	}
	if len(outcome.Deliveries) > 32 {
		return fmt.Errorf("javascript automation exceeded the delivery limit")
	}
	if outcome.Response != nil && (outcome.Response.Status < 100 || outcome.Response.Status > 599) {
		return fmt.Errorf("javascript automation returned an invalid response status")
	}
	for _, delivery := range outcome.Deliveries {
		if strings.TrimSpace(delivery.URL) == "" {
			return fmt.Errorf("javascript automation returned a delivery without a URL")
		}
	}
	return nil
}

const javascriptPrelude = `(function(input) {
  const state = {variables: input.variables || {}, deliveries: [], response: null};
  function freeze(value) {
    if (value && typeof value === 'object') {
      Object.values(value).forEach(freeze);
      Object.freeze(value);
    }
    return value;
  }
  const gateway = Object.freeze({
    event: freeze(input.event || {}),
    get(path) {
      return String(path).split('.').reduce((value, key) => value == null ? undefined : value[key], state.variables);
    },
    set(path, value) {
      const keys = String(path).split('.');
      let target = state.variables;
      for (const key of keys.slice(0, -1)) {
        if (!target[key] || typeof target[key] !== 'object') target[key] = {};
        target = target[key];
      }
      target[keys[keys.length - 1]] = value;
    },
    respond(body = '', status = 200) {
      state.response = {body: String(body), status: Number(status)};
    },
    forward(delivery) {
      state.deliveries.push(delivery);
    }
  });
`

const javascriptSuffix = `
  return JSON.stringify(state);
})`

func (r Runtime) executeLua(ctx context.Context, source string, input Input, outcome *Outcome) error {
	state := lua.NewState(lua.Options{SkipOpenLibs: true})
	defer state.Close()
	state.SetContext(ctx)
	gateway := state.NewTable()
	state.SetField(gateway, "event", toLua(state, input.Event))
	state.SetField(gateway, "get", state.NewFunction(func(state *lua.LState) int {
		value, ok := getPath(outcome.Variables, state.CheckString(1))
		if !ok {
			state.Push(lua.LNil)
			return 1
		}
		state.Push(toLua(state, value))
		return 1
	}))
	state.SetField(gateway, "set", state.NewFunction(func(state *lua.LState) int {
		setPath(outcome.Variables, state.CheckString(1), fromLua(state.Get(2)))
		return 0
	}))
	state.SetField(gateway, "respond", state.NewFunction(func(state *lua.LState) int {
		response := Response{Status: 200, Body: state.OptString(1, "")}
		if state.GetTop() > 1 {
			response.Status = state.CheckInt(2)
		}
		outcome.Response = &response
		return 0
	}))
	state.SetField(gateway, "forward", state.NewFunction(func(state *lua.LState) int {
		var delivery Delivery
		if raw, err := json.Marshal(fromLua(state.CheckTable(1))); err == nil && json.Unmarshal(raw, &delivery) == nil && strings.TrimSpace(delivery.URL) != "" {
			outcome.Deliveries = append(outcome.Deliveries, delivery)
		}
		return 0
	}))
	state.SetGlobal("gateway", gateway)
	if err := state.DoString(source); err != nil {
		return fmt.Errorf("evaluate lua automation: %w", err)
	}
	return nil
}

// WebhookScript is intentionally a compatibility dialect, not an attempt to
// copy Webhook.site's whole product. Its variable and flow primitives compile
// onto Gateway's restricted JavaScript capability object.
func webhookScriptToJavaScript(source string) string {
	replacer := strings.NewReplacer(
		"respond(", "gateway.respond(",
		"set(", "gateway.set(",
		"var(", "gateway.get(",
		" and ", " && ",
		" or ", " || ",
	)
	return regexp.MustCompile(`\bstop\(\)`).ReplaceAllString(replacer.Replace(source), "throw new Error('automation stopped')")
}

func cloneMap(values map[string]any) map[string]any {
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func getPath(values map[string]any, path string) (any, bool) {
	var current any = values
	for _, segment := range strings.Split(path, ".") {
		mapValue, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = mapValue[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func setPath(values map[string]any, path string, value any) {
	segments := strings.Split(path, ".")
	current := values
	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[segment] = next
		}
		current = next
	}
	current[segments[len(segments)-1]] = value
}

func toLua(state *lua.LState, value any) lua.LValue {
	switch value := value.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(value)
	case string:
		return lua.LString(value)
	case int:
		return lua.LNumber(value)
	case int64:
		return lua.LNumber(value)
	case float64:
		return lua.LNumber(value)
	case map[string]any:
		table := state.NewTable()
		for key, entry := range value {
			state.SetField(table, key, toLua(state, entry))
		}
		return table
	case []any:
		table := state.NewTable()
		for _, entry := range value {
			table.Append(toLua(state, entry))
		}
		return table
	default:
		return lua.LString(fmt.Sprint(value))
	}
}

func fromLua(value lua.LValue) any {
	switch value := value.(type) {
	case *lua.LNilType:
		return nil
	case lua.LBool:
		return bool(value)
	case lua.LNumber:
		return float64(value)
	case lua.LString:
		return string(value)
	case *lua.LTable:
		values := map[string]any{}
		value.ForEach(func(key lua.LValue, entry lua.LValue) { values[key.String()] = fromLua(entry) })
		return values
	default:
		return value.String()
	}
}
