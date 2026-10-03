// Package automationextensions is Spinner's composition root for optional
// automation language packages. Build tags add engines without coupling
// transport or planner packages to their implementations.
package automationextensions

import "github.com/webong/gateway/driver/automation"

var installed []automation.Engine

type RunHistoryReader interface {
	ReadRun(string) ([]byte, error)
	RunHistoryConfigured() bool
}

func register(engine automation.Engine) {
	installed = append(installed, engine)
}

// Runtime returns a dispatcher containing only engines selected by build tags.
func Runtime() *automation.Runtime {
	return automation.NewRuntime(installed...)
}

// RunHistory returns the optional local run reader without exposing the shell
// implementation to Spinner's transport or private admin packages.
func RunHistory() RunHistoryReader {
	for _, engine := range installed {
		if reader, ok := engine.(RunHistoryReader); ok && reader.RunHistoryConfigured() {
			return reader
		}
	}
	return nil
}
