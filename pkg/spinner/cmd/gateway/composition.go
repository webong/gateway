package main

import (
	"fmt"

	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
)

// registerStatic installs the compiled-in guests named in config. The available
// set is defined by plugins.txt and generated into staticPlugins, so this
// binary never hardcodes an import list or a language switch.
func registerStatic(names []string) error {
	for _, name := range names {
		factory, ok := staticPlugins[name]
		if !ok {
			return fmt.Errorf("automation engine %q is not compiled in; add it to plugins.txt and run go generate", name)
		}
		engine, err := factory()
		if err != nil {
			return fmt.Errorf("configure %s engine: %w", name, err)
		}
		automationextensions.Register(engine)
	}
	return nil
}
