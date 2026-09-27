// Package runtime implements the optional Lua automation extension.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/webong/gateway/driver/automation"
	lua "github.com/yuin/gopher-lua"
)

const Language automation.Language = "lua"

type Engine struct {
	Timeout time.Duration
}

func New() *Engine { return &Engine{Timeout: 50 * time.Millisecond} }

func (e *Engine) Language() automation.Language { return Language }

func (e *Engine) Execute(ctx context.Context, source string, input automation.Input) (automation.Outcome, error) {
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	outcome := automation.Outcome{Variables: input.Variables}
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
		response := automation.Response{Status: 200, Body: state.OptString(1, "")}
		if state.GetTop() > 1 {
			response.Status = state.CheckInt(2)
		}
		outcome.Response = &response
		return 0
	}))
	state.SetField(gateway, "forward", state.NewFunction(func(state *lua.LState) int {
		var delivery automation.Delivery
		if raw, err := json.Marshal(fromLua(state.CheckTable(1))); err == nil && json.Unmarshal(raw, &delivery) == nil {
			outcome.Deliveries = append(outcome.Deliveries, delivery)
		}
		return 0
	}))
	state.SetField(gateway, "schedule", state.NewFunction(func(state *lua.LState) int {
		var schedule automation.Schedule
		raw, err := json.Marshal(fromLua(state.CheckTable(1)))
		if err != nil {
			state.RaiseError("encode schedule: %v", err)
			return 0
		}
		if err := json.Unmarshal(raw, &schedule); err != nil {
			state.RaiseError("decode schedule: %v", err)
			return 0
		}
		outcome.Schedules = append(outcome.Schedules, schedule)
		return 0
	}))
	state.SetGlobal("gateway", gateway)
	if err := state.DoString(source); err != nil {
		return automation.Outcome{}, fmt.Errorf("evaluate lua automation: %w", err)
	}
	return outcome, nil
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

var _ automation.Engine = (*Engine)(nil)
