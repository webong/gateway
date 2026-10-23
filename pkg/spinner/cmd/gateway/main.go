// Command gateway is Spinner's host binary and automation composition root.
//
// It links the statically selected guests from plugins_gen.go and then loads
// any dynamic guests (native .so or external processes) before starting Spinner.
// Spinner's own packages never import an engine.
package main

import (
	"context"

	"github.com/webong/gateway/pkg/spinner/proxy"
	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
)

func main() {
	cfg := loadConfig()

	if err := registerStatic(cfg.StaticEngines); err != nil {
		panic(err)
	}

	if err := automationextensions.LoadPlugins(context.Background(), automationextensions.LoadConfig{
		NativeGoDir: cfg.NativePluginDir,
		ExternalDir: cfg.ExternalPluginDir,
	}); err != nil {
		panic(err)
	}

	proxy.Main()
}
