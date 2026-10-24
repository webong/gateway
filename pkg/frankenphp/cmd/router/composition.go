package main

import (
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
// its planner runtime, and PHP automation belongs to the standalone host.
// HookScript remains a Go guest and reaches only its declared Planner companion.
func registerStaticEngines() error {
	for _, plugin := range []func() (automation.Engine, error){
		javascript.Plugin,
		lua.Plugin,
		shell.Plugin,
		golang.Plugin,
		hookscript.Plugin,
	} {
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
