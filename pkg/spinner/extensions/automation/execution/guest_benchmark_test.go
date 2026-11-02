//go:build gateway_guest_bench

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webong/ext/pkg/plugin-go/contract"
	"github.com/webong/ext/pkg/plugin-go/contract/host/store"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
	"github.com/webong/gateway/arranger/automation"
	automationextensions "github.com/webong/gateway/pkg/spinner/extensions/automation"
)

// BenchmarkInstalledLuaGuest includes the verified package, child process,
// plugin transport, and real Lua interpreter in both execution modes.
func BenchmarkInstalledLuaGuest(b *testing.B) {
	guestBinary := os.Getenv("GATEWAY_BENCH_LUA_GUEST")
	if guestBinary == "" {
		b.Fatal("GATEWAY_BENCH_LUA_GUEST must point to the built Lua guest")
	}
	for _, mode := range []Mode{ModeGoroutine, ModeErgo} {
		b.Run(string(mode), func(b *testing.B) {
			registry := installedLuaRegistry(b, guestBinary)
			defer registry.UnregisterExtension("webong/lua")
			executor, err := New(registry, Options{Mode: mode, Workers: 4, QueueSize: 16, Timeout: 5 * time.Second})
			if err != nil {
				b.Fatal(err)
			}
			defer executor.Close()
			script := automation.Script{Language: "lua", Source: `gateway.respond("ok")`}
			input := automation.Input{Event: map[string]any{"kind": "benchmark"}, Variables: map[string]any{}}
			b.SetParallelism(2)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					outcome, err := executor.Execute(context.Background(), script, input)
					if err != nil {
						b.Error(err)
						return
					}
					if outcome.Response == nil || outcome.Response.Status != 200 || outcome.Response.Body != "ok" {
						b.Errorf("unexpected Lua outcome: %+v", outcome)
						return
					}
				}
			})
		})
	}
}

func installedLuaRegistry(b *testing.B, binaryPath string) *automationextensions.Registry {
	b.Helper()
	root := b.TempDir()
	directory := filepath.Join(root, "webong%2flua")
	if err := os.MkdirAll(filepath.Join(directory, "bin"), 0o700); err != nil {
		b.Fatal(err)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bin", "spinner"), binary, 0o700); err != nil {
		b.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	manifest := packagekit.Manifest{
		APIVersion: packagekit.Version,
		Descriptor: contract.Descriptor{
			APIVersion: contract.APIVersion,
			Identity:   contract.Identity{ID: "webong/lua", Revision: "benchmark-r1", Version: "1.0.0"},
			Contracts: []contract.Contract{{
				ContractRef: automationextensions.AutomationContract,
				Operations:  []contract.Operation{{Name: "execute", Surface: "lua"}},
			}},
		},
		Artifacts:   []packagekit.Artifact{{Name: "spinner-guest", Path: "bin/spinner", SHA256: hex.EncodeToString(digest[:])}},
		Entrypoints: []packagekit.Entrypoint{{Name: "spinner", Runtime: "process", Artifact: "spinner-guest", Protocols: []string{contract.APIVersion}}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.json"), data, 0o600); err != nil {
		b.Fatal(err)
	}
	packages, err := store.New(root, store.Limits{})
	if err != nil {
		b.Fatal(err)
	}
	installed, err := packages.Load("webong/lua")
	if err != nil {
		b.Fatal(err)
	}
	registry := automationextensions.NewRegistry()
	if err := automationextensions.LoadExternalInstalled(context.Background(), registry, installed); err != nil {
		b.Fatal(err)
	}
	return registry
}
