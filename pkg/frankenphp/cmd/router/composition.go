package main

import (
	"context"
	"os"
	"strings"

	"github.com/webong/gateway/driver/automation"
	golang "github.com/webong/gateway/pkg/golang/runtime"
	hookscript "github.com/webong/gateway/pkg/hookscript/runtime"
	javascript "github.com/webong/gateway/pkg/javascript/runtime"
	lua "github.com/webong/gateway/pkg/lua/runtime"
	shell "github.com/webong/gateway/pkg/shell/runtime"
	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
)

// registerStaticEngines installs the guest automation engines this Router
// ships with. This command is a composition root: Spinner never imports an
// engine package itself.
//
// The PHP engine is intentionally absent: this Router already runs PHP through
// its planner runtime, and raw PHP automation belongs only in an explicitly
// composed host. HookScript remains bundled here until combined Router hosts
// gain the same live artifact reconciler as separately hosted Spinner.
func registerStaticEngines() error {
	plugins := []func() (automation.Engine, error){
		javascript.Plugin,
		lua.Plugin,
		golang.Plugin,
		hookscript.Plugin,
	}
	if shellEnabled() {
		plugins = append(plugins, shell.Plugin)
	}
	for _, plugin := range plugins {
		engine, err := plugin()
		if err != nil {
			return err
		}
		if err := automationextensions.Register(engine); err != nil {
			return err
		}
	}
	return nil
}

// loadDynamicEngines installs process and native guests selected at deploy
// time. Bundled Routers use the same extension store contract as standalone
// Spinner; the composition root, rather than Spinner's core, owns loading.
func loadDynamicEngines(ctx context.Context) error {
	return automationextensions.LoadAll(ctx)
}

func shellEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GATEWAY_SHELL_ENABLED"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
