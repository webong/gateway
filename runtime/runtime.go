// Package runtime implements the optional JavaScript automation extension.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/webong/gateway/driver/automation"
	"github.com/webong/gateway/pkg/javascript/runtime/qjs"
)

const Language automation.Language = "javascript"

type Engine struct {
	Timeout     time.Duration
	MemoryLimit uint64
}

func New() *Engine {
	return &Engine{Timeout: 50 * time.Millisecond, MemoryLimit: 4 * 1024 * 1024}
}

func (e *Engine) Language() automation.Language { return Language }

func (e *Engine) Execute(ctx context.Context, source string, input automation.Input) (automation.Outcome, error) {
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}
	memoryLimit := e.MemoryLimit
	if memoryLimit == 0 {
		memoryLimit = 4 * 1024 * 1024
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	outcome := automation.Outcome{Variables: input.Variables}
	inputJSON, err := json.Marshal(map[string]any{
		"event":     input.Event,
		"variables": outcome.Variables,
	})
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("marshal automation event: %w", err)
	}
	outputJSON, err := qjs.Run(ctx, javascriptPrelude+source+javascriptSuffix, inputJSON, memoryLimit, timeout)
	if err != nil {
		return automation.Outcome{}, fmt.Errorf("evaluate javascript automation: %w", err)
	}
	if err := json.Unmarshal(outputJSON, &outcome); err != nil {
		return automation.Outcome{}, fmt.Errorf("decode javascript automation outcome: %w", err)
	}
	return outcome, nil
}

const javascriptPrelude = `(function(input) {
  const state = {variables: input.variables || {}, deliveries: [], schedules: [], response: null};
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
    },
    schedule(definition) {
      state.schedules.push({...definition});
    }
  });
`

const javascriptSuffix = `
  return JSON.stringify(state);
})`

var _ automation.Engine = (*Engine)(nil)

// Plugin returns the engine for composition roots and dynamic loaders.
func Plugin() (automation.Engine, error) { return New(), nil }
