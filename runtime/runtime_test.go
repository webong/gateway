//go:build !windows

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

	"github.com/webong/gateway/arranger/automation"
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

func TestRemoteHelperKeepsSpinnerRunID(t *testing.T) {
	root := t.TempDir()
	writeHook(t, root, "github/deploy", "#!/bin/sh\nprintf '{\"response\":{\"status\":202}}'\n")
	engine, err := New(Config{HooksDir: root})
	if err != nil { t.Fatal(err) }
	ctx, err := WithRunID(context.Background(), "aabbccddeeff00112233445566778899")
	if err != nil { t.Fatal(err) }
	outcome, err := engine.Execute(ctx, "github/deploy", automation.Input{})
	if err != nil { t.Fatal(err) }
	if outcome.RunID != "aabbccddeeff00112233445566778899" {
		t.Fatalf("remote helper changed Spinner run ID to %q", outcome.RunID)
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
	outputEngine, err := New(Config{HooksDir: root, Timeout: 5 * time.Second, MaxOutputBytes: 5})
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

func TestShellRunHistoryRedactsOutputUnlessEnabled(t *testing.T) {
	root := t.TempDir()
	logs := t.TempDir()
	if err := os.Chmod(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	writeHook(t, root, "secret", "#!/bin/sh\nprintf 'private output'\necho 'private error' >&2\n")
	engine, err := New(Config{HooksDir: root, RunLogDir: logs, OutputMode: ModeBuffered})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := engine.Execute(context.Background(), "secret", automation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	redacted, err := engine.ReadRun(outcome.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(redacted), "private") || !strings.Contains(string(redacted), `"hook":"secret"`) {
		t.Fatalf("run history did not redact output: %s", redacted)
	}
	visible, err := New(Config{HooksDir: root, RunLogDir: logs, ExposeRunOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	record, err := visible.ReadRun(outcome.RunID)
	if err != nil || !strings.Contains(string(record), "private output") || !strings.Contains(string(record), "private error") {
		t.Fatalf("opted-in run output unavailable: %s, %v", record, err)
	}
	if _, err := visible.ReadRun("../secret"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid run ID was accepted: %v", err)
	}
	if err := os.Symlink(filepath.Join(logs, outcome.RunID+".json"), filepath.Join(logs, strings.Repeat("a", 32)+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := visible.ReadRun(strings.Repeat("a", 32)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlinked run record was accepted: %v", err)
	}
}

func TestShellRunRetentionPrunesOnlyExpiredRunRecordsWhenEnabled(t *testing.T) {
	root := t.TempDir()
	logs := t.TempDir()
	if err := os.Chmod(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	writeHook(t, root, "ok", "#!/bin/sh\nprintf '{\"response\":{\"status\":200}}'\n")
	withoutRetention, err := New(Config{HooksDir: root, RunLogDir: logs})
	if err != nil {
		t.Fatal(err)
	}
	oldRun, err := withoutRetention.Execute(context.Background(), "ok", automation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(logs, oldRun.RunID+".json")
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(logs, "operator-notes.json")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(unrelated, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(logs, strings.Repeat("a", 32)+".json")
	if err := os.Symlink(unrelated, link); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{HooksDir: root, RunLogDir: logs}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("restart must preserve records when retention is disabled:", err)
	}
	if _, err := New(Config{HooksDir: root, RunRetention: time.Hour}); err == nil {
		t.Fatal("retention without a private run directory must fail")
	}
	withRetention, err := New(Config{HooksDir: root, RunLogDir: logs, RunRetention: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired run record should be removed, got %v", err)
	}
	for _, path := range []string{unrelated, link} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("non-run file should be preserved: %s: %v", path, err)
		}
	}
	newRun, err := withRetention.Execute(context.Background(), "ok", automation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(logs, newRun.RunID+".json")); err != nil {
		t.Fatalf("new run should be retained: %v", err)
	}
}

func TestShellRunRetentionEnvironmentRequiresPositiveDuration(t *testing.T) {
	root := t.TempDir()
	logs := t.TempDir()
	if err := os.Chmod(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(logs, strings.Repeat("b", 32)+".json")
	if err := os.WriteFile(oldPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_SHELL_HOOKS_DIR", root)
	t.Setenv("GATEWAY_SHELL_RUN_LOG_DIR", logs)
	t.Setenv("GATEWAY_SHELL_RUN_RETENTION", "1h")
	if _, err := NewFromEnvironment(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configured retention did not prune expired record: %v", err)
	}
	t.Setenv("GATEWAY_SHELL_RUN_RETENTION", "-1h")
	if _, err := NewFromEnvironment(); err == nil {
		t.Fatal("negative shell run retention should fail configuration")
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
