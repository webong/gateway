// Command gateway is Spinner's host binary and automation composition root.
//
// Automation extensions are installed packages loaded from the extension
// store. This host never imports or compiles a concrete automation runtime.
// Spinner's own packages never import an engine.
package main

import (
	"context"

	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
	"github.com/webong/gateway/pkg/spinner/proxy"
)

func main() {
	cfg := loadConfig()

	if err := automationextensions.LoadPlugins(context.Background(), automationextensions.LoadConfig{
		NativeGoDir: cfg.NativePluginDir,
		ExternalDir: cfg.ExternalPluginDir,
	}); err != nil {
		panic(err)
	}

	proxy.Main()
}
