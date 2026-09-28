package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webong/gateway/driver/automation"
)

func TestShellHookReceivesJSONAndReturnsAnOutcome(t *testing.T) {
	root := t.TempDir()
	writeHook(t, root, "github/deploy", "#!/bin/sh\nread input\nprintf '{\"response\":{\"status\":202,\"body\":\"accepted\"}}'\n")
	engine, err := New(Config{HooksDir: root})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := automation.NewRuntime(engine).Execute(context.Background(), automation.Script{
		Language: Language,
		Source:   "github/deploy",
	}, automation.Input{Event: map[string]any{"event": "paid"}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.RunID == "" || outcome.Response == nil || outcome.Response.Status != 202 || outcome.Response.Body != "accepted" {
		t.Fatalf("unexpected outcome %#v", outcome)
	}
}

func TestShellHookRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeHook(t, outside, "escape", "#!/bin/sh\nprintf '{}'\n")
	if err := os.Symlink(filepath.Join(outside, "escape"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{HooksDir: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"../escape", "escape"} {
		_, err := engine.Execute(context.Background(), source, automation.Input{})
		if err == nil || !strings.Contains(err.Error(), "shell hook") {
			t.Fatalf("expected shell hook path rejection for %q, got %v", source, err)
		}
	}
}

func TestShellHookEnforcesTimeoutAndOutputLimit(t *testing.T) {
	root := t.TempDir()
	writeHook(t, root, "slow", "#!/bin/sh\nsleep 1\nprintf '{}'\n")
	writeHook(t, root, "large", "#!/bin/sh\nprintf '1234567890'\n")
	engine, err := New(Config{HooksDir: root, Timeout: 10 * time.Millisecond, MaxOutputBytes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(context.Background(), "slow", automation.Input{}); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	outputEngine, err := New(Config{HooksDir: root, Timeout: time.Second, MaxOutputBytes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outputEngine.Execute(context.Background(), "large", automation.Input{}); err == nil || !strings.Contains(err.Error(), "output exceeded") {
		t.Fatalf("expected output-limit error, got %v", err)
	}
}

func TestShellHookRequiresJSONAndRejectsSchedules(t *testing.T) {
	root := t.TempDir()
	writeHook(t, root, "plain", "#!/bin/sh\nprintf 'not json'\n")
	writeHook(t, root, "schedule", "#!/bin/sh\nprintf '{\"schedules\":[{\"key\":\"x\"}]}'\n")
	engine, err := New(Config{HooksDir: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"plain", "schedule"} {
		if _, err := engine.Execute(context.Background(), source, automation.Input{}); err == nil {
			t.Fatalf("expected rejection for %s", source)
		}
	}
}

func TestShellHookLimitsConcurrentRuns(t *testing.T) {
	root := t.TempDir()
	writeHook(t, root, "wait", "#!/bin/sh\nsleep 0.3\nprintf '{}'\n")
	engine, err := New(Config{HooksDir: root, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := engine.Execute(context.Background(), "wait", automation.Input{})
		done <- err
	}()
	deadline := time.After(time.Second)
	for len(engine.slots) == 0 {
		select {
		case <-deadline:
			t.Fatal("first hook did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if _, err := engine.Execute(context.Background(), "wait", automation.Input{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected concurrency rejection, got %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestShellHookQueuesOnlyConfiguredNumberOfRuns(t *testing.T) {
	root := t.TempDir()
	writeHook(t, root, "wait", "#!/bin/sh\nsleep 0.2\nprintf '{}'\n")
	engine, err := New(Config{HooksDir: root, MaxConcurrent: 1, MaxQueued: 1})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() { _, err := engine.Execute(context.Background(), "wait", automation.Input{}); done <- err }()
	waitForCount := func(queue chan struct{}, count int) {
		t.Helper()
		deadline := time.After(time.Second)
		for len(queue) != count {
			select {
			case <-deadline:
				t.Fatalf("expected %d pending runs, got %d", count, len(queue))
			default:
				time.Sleep(time.Millisecond)
			}
		}
	}
	waitForCount(engine.slots, 1)
	go func() { _, err := engine.Execute(context.Background(), "wait", automation.Input{}); done <- err }()
	waitForCount(engine.queue, 1)
	if _, err := engine.Execute(context.Background(), "wait", automation.Input{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected full queue rejection, got %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestShellHookBufferedResponseAndPrivateRunRecord(t *testing.T) {
	root := t.TempDir()
	logs := t.TempDir()
	if err := os.Chmod(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	writeHook(t, root, "reject", "#!/bin/sh\ncat >/dev/null\nprintf 'denied'\necho 'private error' >&2\nexit 118\n")
	engine, err := New(Config{HooksDir: root, RunLogDir: logs, OutputMode: ModeBuffered})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := automation.NewRuntime(engine).Execute(context.Background(), automation.Script{
		Language: Language, Source: "reject",
	}, automation.Input{Event: map[string]any{"delivery_id": "delivery-123"}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Response == nil || outcome.Response.Status != 418 || outcome.Response.Body != "denied" {
		t.Fatalf("unexpected buffered response: %#v", outcome.Response)
	}
	id := outcome.Response.Headers["X-Gateway-Shell-Run-Id"][0]
	data, err := os.ReadFile(filepath.Join(logs, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(logs, id+".json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("run record is not private: %v %v", info, err)
	}
	var record runRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.DeliveryID != "delivery-123" || record.ExitCode != 118 || record.Stderr != "private error\n" {
		t.Fatalf("unexpected run record: %#v", record)
	}
}

func TestShellHookStreamsBeforeProcessCompletes(t *testing.T) {
	root := t.TempDir()
	writeHook(t, root, "progress", "#!/bin/sh\nprintf 'started'\nsleep 1\nprintf 'finished'\n")
	structured, err := New(Config{HooksDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := structured.OpenStream(context.Background(), "progress", automation.Input{}); err == nil {
		t.Fatal("structured outcomes must not be exposed as raw stdout")
	}
	engine, err := New(Config{HooksDir: root, Timeout: 2 * time.Second, OutputMode: ModeBuffered})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := engine.OpenStream(context.Background(), "progress", automation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case first := <-stream.Chunks:
		if string(first) != "started" {
			t.Fatalf("unexpected first chunk %q", first)
		}
	case <-time.After(700 * time.Millisecond):
		t.Fatal("first chunk was buffered until hook completion")
	}
	var rest string
	for chunk := range stream.Chunks {
		rest += string(chunk)
	}
	if rest != "finished" || <-stream.Done != nil {
		t.Fatalf("unexpected stream ending: %q", rest)
	}
}

func writeHook(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}
