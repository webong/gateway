// Package automationextensions is Spinner's composition root for optional
// automation language packages. Build tags add engines without coupling
// transport or planner packages to their implementations.
package automationextensions

import (
	"context"
	"github.com/webong/gateway/driver/automation"
	"net/http"
)

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

// ConfigureShellSSH resolves write-only Planner credentials through the same
// private connection used by the host. When Shell is not installed this is a no-op.
func ConfigureShellSSHHTTP(origin, token string, client *http.Client, localTransport bool) error {
	for _, engine := range installed {
		if configurable, ok := engine.(interface {
			SetSSHResolverHTTP(string, string, *http.Client, bool) error
		}); ok {
			if err := configurable.SetSSHResolverHTTP(origin, token, client, localTransport); err != nil {
				return err
			}
		}
	}
	return nil
}

func ConfigureShellSSHHandler(handler http.Handler, token string) error {
	for _, engine := range installed {
		if configurable, ok := engine.(interface {
			SetSSHResolverHandler(http.Handler, string) error
		}); ok {
			if err := configurable.SetSSHResolverHandler(handler, token); err != nil {
				return err
			}
		}
	}
	return nil
}

func ContextWithShellSSHHandler(ctx context.Context, handler http.Handler, token string) (context.Context, error) {
	for _, engine := range installed {
		if configurable, ok := engine.(interface {
			ContextWithSSHResolverHandler(context.Context, http.Handler, string) (context.Context, error)
		}); ok {
			var err error
			ctx, err = configurable.ContextWithSSHResolverHandler(ctx, handler, token)
			if err != nil {
				return nil, err
			}
		}
	}
	return ctx, nil
}
