package automationextensions

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	packagekit "github.com/webong/ext/pkg/plugin-go/contract/package"
	"github.com/webong/ext/pkg/plugin-go/contract/transport/jsonline"
	"github.com/webong/gateway/driver/automation"
	"github.com/webong/gateway/driver/companion"
	companionextensions "github.com/webong/gateway/pkg/spinner/extensions/companion"
)

func TestArtifactGuestProcess(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_EXTENSION_GUEST") != "1" {
		return
	}
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var manifest packagekit.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	guest, err := plugin.NewGuest(manifest.Descriptor, plugin.GuestOptions{Handler: func(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
		var input ExecuteRequest
		if err := json.Unmarshal(request.Payload, &input); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(30 * time.Second)
		invocation := companion.Invocation{
			ProtocolVersion: companion.ProtocolVersion,
			Action:          "gateway.hookscript.execute", CompanionVersion: "1.0.0",
			ExecutionID: "installed-hookscript", IdempotencyKey: "installed-hookscript",
			Deadline: &deadline, Input: map[string]any{"source": input.Source, "event": input.Event},
		}
		payload, _ := json.Marshal(invocation)
		response, err := plugin.ReverseFrom(ctx).Call(ctx, companionextensions.Contract, invocation.Action, payload)
		if err != nil {
			return nil, err
		}
		var result companion.Result
		if err := json.Unmarshal(response, &result); err != nil {
			return nil, err
		}
		return json.Marshal(ExecuteResponse{Outcome: automation.Outcome{Variables: result.Output}})
	}})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := jsonline.ServeStdio(context.Background(), guest); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func TestArtifactReconcilerInstallsHookScriptAfterSpinnerStarted(t *testing.T) {
	plugins := NewRegistry()
	runtime := plugins.Runtime()
	if runtime.Supports("webhookscript") {
		t.Fatal("HookScript was present before the desired artifact was reconciled")
	}

	companionextensions.Configure(staticTestRunner{})
	t.Cleanup(func() { companionextensions.Configure(nil) })
	archive := hookScriptTestArchive(t)
	archiveDigest := sha256.Sum256(archive)
	var enabled atomic.Bool
	enabled.Store(true)

	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/_internal/gateway/extensions":
			if request.Header.Get("X-Gateway-Internal") != "private-token" {
				http.NotFound(writer, request)
				return
			}
			data := []map[string]any{}
			if enabled.Load() {
				data = append(data, map[string]any{
					"extension": "webong/hookscript", "version": "1.0.0", "digest": nil, "revision": 1,
					"artifact": map[string]any{"url": server.URL + "/hookscript.tar.gz", "sha256": hex.EncodeToString(archiveDigest[:])},
				})
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": data})
		case "/hookscript.tar.gz":
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write(archive)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	reconciler, err := NewArtifactReconciler(server.URL, "private-token", t.TempDir(), server.Client(), server.Client(), plugins)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !runtime.Supports("webhookscript") {
		t.Fatal("running Spinner did not advertise installed HookScript")
	}
	outcome, err := runtime.Execute(t.Context(), automation.Script{
		Language: "webhookscript", Source: "respond('installed')",
	}, automation.Input{Event: map[string]any{"delivery_id": "delivery-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Variables["executed"] != true {
		t.Fatalf("HookScript did not invoke Planner companion: %+v", outcome)
	}

	enabled.Store(false)
	if err := reconciler.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runtime.Supports("webhookscript") {
		t.Fatal("deactivated HookScript remained advertised by the running Spinner")
	}
}

func TestArtifactReconcilerRejectsTamperedDistributionBeforeLoadingGuest(t *testing.T) {
	plugins := NewRegistry()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/_internal/gateway/extensions" {
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{
				"extension": "webong/hookscript", "version": "1.0.0", "digest": nil, "revision": 1,
				"artifact": map[string]any{"url": "https://artifact.invalid/hookscript.tar.gz", "sha256": strings.Repeat("0", 64)},
			}}})
			return
		}
		http.NotFound(writer, request)
	}))
	t.Cleanup(server.Close)
	artifactClient := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader("tampered")), Request: request,
		}, nil
	})
	reconciler, err := NewArtifactReconciler(
		server.URL, "private-token", t.TempDir(), server.Client(), artifactClient, plugins,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(t.Context()); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("tampered artifact was not rejected: %v", err)
	}
	if plugins.Runtime().Supports("webhookscript") {
		t.Fatal("tampered artifact registered a Spinner guest")
	}
}

func TestArtifactReconcilerRemovesCachedGuestThatIsNoLongerDesired(t *testing.T) {
	plugins := NewRegistry()
	engine := lateEngine{language: "webhookscript"}
	plugins.Register(NewEngineAdapter(engine, EngineDescriptor(engine, plugin.Identity{
		ID: "webong/hookscript", Revision: "cached-r1", Version: "1.0.0",
	})))
	if !plugins.Runtime().Supports("webhookscript") {
		t.Fatal("test did not begin with a cached guest")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": []any{}})
	}))
	t.Cleanup(server.Close)
	reconciler, err := NewArtifactReconciler(
		server.URL, "private-token", t.TempDir(), server.Client(), server.Client(), plugins,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if plugins.Runtime().Supports("webhookscript") {
		t.Fatal("cached inactive guest remained advertised after desired-state reconciliation")
	}
}

func TestArtifactReconcilerClosesCachedGuestsWhenPlannerDesiredStateIsUnavailable(t *testing.T) {
	plugins := NewRegistry()
	engine := lateEngine{language: "webhookscript"}
	plugins.Register(NewEngineAdapter(engine, EngineDescriptor(engine, plugin.Identity{
		ID: "webong/hookscript", Revision: "cached-r1", Version: "1.0.0",
	})))
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	reconciler, err := NewArtifactReconciler(
		server.URL, "private-token", t.TempDir(), server.Client(), server.Client(), plugins,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(t.Context()); err == nil {
		t.Fatal("unavailable desired state was accepted")
	}
	if plugins.Runtime().Supports("webhookscript") {
		t.Fatal("cached guest remained active while Planner desired state was unavailable")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func hookScriptTestArchive(t *testing.T) []byte {
	t.Helper()
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(testBinary)
	if err != nil {
		t.Fatal(err)
	}
	wrapper := []byte("#!/bin/sh\nGATEWAY_TEST_EXTENSION_GUEST=1 exec \"$PWD/bin/guest\" -test.run=TestArtifactGuestProcess\n")
	planner := []byte("<?php // Planner owns and executes this artifact.\n")
	descriptor := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   plugin.Identity{ID: "webong/hookscript", Revision: "test-r1", Version: "1.0.0"},
		Contracts: []plugin.Contract{{
			ContractRef: AutomationContract,
			Operations:  []plugin.Operation{{Name: "execute", Surface: "webhookscript"}},
		}},
		Reverse: []plugin.Contract{{
			ContractRef: companionextensions.Contract,
			Operations:  []plugin.Operation{{Name: "gateway.hookscript.execute"}},
		}},
	}
	companionDescriptor := plugin.Descriptor{
		APIVersion: plugin.APIVersion,
		Identity:   descriptor.Identity,
		Contracts: []plugin.Contract{{
			ContractRef: companionextensions.Contract,
			Operations:  []plugin.Operation{{Name: "gateway.hookscript.execute"}},
		}},
	}
	companionMetadata, _ := json.Marshal(map[string]any{"descriptor": companionDescriptor})
	manifest := packagekit.Manifest{
		APIVersion: packagekit.Version,
		Descriptor: descriptor,
		Metadata:   map[string]json.RawMessage{"gateway.companion/v1": companionMetadata},
		Artifacts: []packagekit.Artifact{
			{Name: "spinner-guest", Path: "bin/spinner", SHA256: digestBytes(wrapper)},
			{Name: "test-guest", Path: "bin/guest", SHA256: digestBytes(binary)},
			{Name: "planner-companion", Path: "bin/companion", SHA256: digestBytes(planner)},
		},
		Entrypoints: []packagekit.Entrypoint{
			{Name: "spinner", Runtime: "process", Artifact: "spinner-guest", Protocols: []string{plugin.APIVersion}},
			{Name: "planner", Runtime: "php-jsonline", Artifact: "planner-companion", Protocols: []string{plugin.APIVersion}},
			{Name: "test-helper", Runtime: "process", Artifact: "test-guest", Protocols: []string{plugin.APIVersion}},
		},
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"webong%2fhookscript/plugin.json":   manifestJSON,
		"webong%2fhookscript/bin/spinner":   wrapper,
		"webong%2fhookscript/bin/guest":     binary,
		"webong%2fhookscript/bin/companion": planner,
	}
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
