package main

import (
	"github.com/webong/gateway/driver/automation"
	golang "github.com/webong/gateway/pkg/golang/runtime"
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
func registerStaticEngines() error {
	for _, plugin := range []func() (automation.Engine, error){
		javascript.Plugin,
		lua.Plugin,
		shell.Plugin,
		golang.Plugin,
	} {
		engine, err := plugin()
		if err != nil {
			return err
		}
		automationextensions.Register(engine)
	}
	return nil
}
