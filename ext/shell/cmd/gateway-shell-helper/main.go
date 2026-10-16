// gateway-shell-helper is the fixed authorized_keys forced command on a remote
// Shell worker. It never accepts a command or executable path from SSH argv.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/webong/gateway/driver/automation"
	shell "github.com/webong/gateway/ext/shell/runtime"
)

const maxInputBytes = 1 << 20

type request struct {
	Hook      string         `json:"hook"`
	RunID     string         `json:"run_id"`
	Event     map[string]any `json:"event"`
	Variables map[string]any `json:"variables"`
}

func main() {
	if os.Getenv("SSH_ORIGINAL_COMMAND") != "gateway-shell-helper" {
		os.Exit(1)
	}
	if mode := os.Getenv("GATEWAY_SHELL_OUTPUT_MODE"); mode != "" && mode != shell.ModeOutcome {
		os.Exit(1)
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, maxInputBytes+1))
	if err != nil || len(body) > maxInputBytes {
		os.Exit(1)
	}
	var input request
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.RunID == "" || input.Hook == "" {
		os.Exit(1)
	}
	engine, err := shell.NewFromEnvironment()
	if err != nil {
		os.Exit(1)
	}
	ctx, err := shell.WithRunID(context.Background(), input.RunID)
	if err != nil {
		os.Exit(1)
	}
	outcome, err := automation.NewRuntime(engine).Execute(ctx, automation.Script{
		Language: shell.Language, Source: input.Hook,
	}, automation.Input{Event: input.Event, Variables: input.Variables})
	if err != nil {
		os.Exit(1)
	}
	if json.NewEncoder(os.Stdout).Encode(outcome) != nil {
		os.Exit(1)
	}
}
