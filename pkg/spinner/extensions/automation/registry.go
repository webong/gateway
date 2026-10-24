// Package automationextensions is Spinner's host-side automation registry.
//
// Spinner is a plugin host: it depends only on the automation driver contract
// and the portable plugin contract. Automation engines (JavaScript, Lua, Shell,
// Go, PHP, HookScript, or third-party engines) are guests and are registered
// here either in-process by an external composition root or loaded at runtime
// from native/shared objects and external plugin processes.
//
// This package must never import a concrete engine package.
package automationextensions

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host"
	"github.com/webong/ext/pkg/plugin-go/contract/host/nativego"
	pluginprocess "github.com/webong/ext/pkg/plugin-go/contract/host/process"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/inprocess"
	"github.com/webong/gateway/driver/automation"
	companionextensions "github.com/webong/gateway/pkg/spinner/extensions/companion"
)

var registry = NewRegistry()

// Register installs an in-process guest engine. Composition roots outside
// Spinner call it so that Spinner never imports an engine package directly.
func Register(engine automation.Engine) error {
	if engine == nil {
		return contract.ErrInvalid
	}
	descriptor := EngineDescriptor(engine, contract.Identity{
		ID:       "builtin/" + string(engine.Language()),
		Revision: "v0",
	})
	adapter := NewEngineAdapter(engine, descriptor)
	backend, err := inprocess.New(adapter)
	if err != nil {
		return fmt.Errorf("open built-in %s plugin transport: %w", engine.Language(), err)
	}
	session, err := contract.Open(context.Background(), descriptor, contract.Options{
		Verify:    func(context.Context, contract.Descriptor) error { return nil },
		Connect:   func(context.Context, contract.Descriptor) (contract.Backend, error) { return backend, nil },
		Authorize: func(context.Context, contract.Request) error { return nil },
		Reverse:   companionextensions.DefaultHost.ReverseOptions(),
	})
	if err != nil {
		_ = backend.Close()
		return fmt.Errorf("open built-in %s plugin session: %w", engine.Language(), err)
	}
	wrapped, err := NewSessionEngine(session)
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = session.Close(closeCtx)
		cancel()
		return fmt.Errorf("wrap built-in %s plugin session: %w", engine.Language(), err)
	}
	registry.Register(NewEngineAdapter(wrapped, descriptor))
	return nil
}

// Runtime returns a dispatcher containing every engine registered so far.
func Runtime() *Registry {
	return registry.Runtime()
}

// LoadConfig configures runtime guest loading.
type LoadConfig struct {
	// NativeGoDir holds .so guests built with -buildmode=plugin.
	NativeGoDir string
	// ExternalDir holds externpo plugin guests launched via the ext host.
	ExternalDir string
}

// DefaultLoadConfig reads the loader directories from the environment.
func DefaultLoadConfig() LoadConfig {
	return LoadConfig{
		NativeGoDir: os.Getenv("GATEWAY_PLUGIN_DIR_NATIVE"),
		ExternalDir: os.Getenv("GATEWAY_PLUGIN_DIR"),
	}
}

// LoadAll loads runtime guests using the default configuration.
func LoadAll(ctx context.Context) error {
	return LoadPlugins(ctx, DefaultLoadConfig())
}

// LoadPlugins discovers and loads guest engines from native and external
// plugin directories. In-process engines registered via Register are untouched.
func LoadPlugins(ctx context.Context, cfg LoadConfig) error {
	if cfg.NativeGoDir != "" {
		if err := loadNativeGoPlugins(ctx, cfg.NativeGoDir); err != nil {
			return fmt.Errorf("load native Go plugins: %w", err)
		}
	}
	if cfg.ExternalDir != "" {
		if err := loadExternalPlugins(ctx, cfg.ExternalDir); err != nil {
			return fmt.Errorf("load external plugins: %w", err)
		}
	}
	return nil
}

// loadNativeGoPlugins loads .so guests built with -buildmode=plugin. Each guest
// exports contract.Symbol and returns a plugin.Backend that Spinner wraps as an
// automation engine.
func loadNativeGoPlugins(ctx context.Context, dir string) error {
	if !nativego.Supported() {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read native plugin dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".so") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		backend, err := nativego.Open(ctx, path)
		if err != nil {
			fmt.Printf("failed to load native plugin %s: %v\n", path, err)
			continue
		}
		descriptor, err := backend.Handshake(ctx)
		if err != nil {
			_ = backend.Close()
			fmt.Printf("native plugin %s handshake failed: %v\n", e.Name(), err)
			continue
		}
		session, err := contract.Open(ctx, descriptor, contract.Options{
			Verify:    func(context.Context, contract.Descriptor) error { return nil },
			Connect:   func(context.Context, contract.Descriptor) (contract.Backend, error) { return backend, nil },
			Authorize: func(context.Context, contract.Request) error { return nil },
			Reverse:   companionextensions.DefaultHost.ReverseOptions(),
		})
		if err != nil {
			_ = backend.Close()
			fmt.Printf("native plugin %s session failed: %v\n", e.Name(), err)
			continue
		}
		engine, err := NewSessionEngine(session)
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = session.Close(closeCtx)
			cancel()
			fmt.Printf("native plugin %s is not an automation guest: %v\n", e.Name(), err)
			continue
		}
		if err := Register(engine); err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = session.Close(closeCtx)
			cancel()
			fmt.Printf("native plugin %s registration failed: %v\n", e.Name(), err)
			continue
		}
		fmt.Printf("loaded native Go plugin: %s (lang=%s)\n", e.Name(), engine.Language())
	}
	return nil
}

// loadExternalPlugins loads plugin packages installed in an ext store and
// launches each as a supervised child process.
func loadExternalPlugins(ctx context.Context, dir string) error {
	s, err := store.New(dir, store.Limits{})
	if err != nil {
		return fmt.Errorf("open plugin store: %w", err)
	}
	installed, err := s.List()
	if err != nil {
		return fmt.Errorf("list plugins: %w", err)
	}
	for _, inst := range installed {
		if err := loadExternalInstalled(ctx, registry, inst); err != nil {
			fmt.Printf("failed to load plugin %s: %v\n", inst.Name(), err)
		}
	}
	return nil
}

func loadExternalInstalled(ctx context.Context, target *Registry, inst store.Installed) error {
	if !SupportsAutomation(inst.Manifest.Descriptor) {
		return nil
	}
	launch, err := spinnerLaunch(inst)
	if err != nil {
		return fmt.Errorf("spinner entrypoint is invalid: %w", err)
	}
	proc, err := host.Open(ctx, inst, host.Options{
		Reverse: companionextensions.DefaultHost.ReverseOptions(),
		Launch:  launch,
	})
	if err != nil {
		return err
	}
	engine, err := NewProcessEngine(ctx, proc, inst.Manifest.Descriptor)
	if err != nil {
		_ = proc.Close()
		return fmt.Errorf("not an automation guest: %w", err)
	}
	target.Register(NewEngineAdapter(engine, inst.Manifest.Descriptor))
	fmt.Printf("loaded external plugin: %s (lang=%s)\n", inst.Name(), engine.Language())
	return nil
}

// spinnerLaunch selects this plane's entrypoint from a combined package. A nil
// command preserves ext's first-entrypoint behavior for legacy packages.
func spinnerLaunch(inst store.Installed) (*pluginprocess.Command, error) {
	hasNamedEntrypoint := false
	for _, entrypoint := range inst.Manifest.Entrypoints {
		hasNamedEntrypoint = hasNamedEntrypoint || entrypoint.Name != ""
		if entrypoint.Name != "spinner" {
			continue
		}
		path, err := inst.ArtifactPath(entrypoint.Artifact)
		if err != nil {
			return nil, err
		}
		return &pluginprocess.Command{Path: path, Dir: inst.Directory}, nil
	}
	if hasNamedEntrypoint {
		return nil, errors.New("package does not declare a spinner entrypoint")
	}
	return nil, nil
}

// RunHistory returns the optional local run reader without exposing a concrete
// engine implementation to Spinner's transport or private admin packages.
type RunHistoryReader interface {
	ReadRun(string) ([]byte, error)
	RunHistoryConfigured() bool
}

func RunHistory() RunHistoryReader {
	for _, e := range registry.All() {
		if reader, ok := e.(RunHistoryReader); ok && reader.RunHistoryConfigured() {
			return reader
		}
	}
	return nil
}

// ConfigureShellSSHHTTP resolves write-only Planner credentials through the same
// private connection used by the host. When Shell is not installed this is a no-op.
func ConfigureShellSSHHTTP(origin, token string, client *http.Client, localTransport bool) error {
	for _, e := range registry.All() {
		if configurable, ok := e.(interface {
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
	for _, e := range registry.All() {
		if configurable, ok := e.(interface {
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
	for _, e := range registry.All() {
		if configurable, ok := e.(interface {
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
