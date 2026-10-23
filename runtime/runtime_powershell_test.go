package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/webong/gateway/driver/automation"
)

func TestPowerShellConfigurationRequiresExplicitExecutable(t *testing.T) {
	root := t.TempDir()
	for _, config := range []Config{
		{HooksDir: root, Interpreter: "bash"},
		{HooksDir: root, Interpreter: InterpreterPowerShell},
		{HooksDir: root, Interpreter: InterpreterPowerShell, PowerShellPath: "pwsh"},
		{HooksDir: root, PowerShellPath: filepath.Join(root, "pwsh")},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("expected invalid configuration to fail: %#v", config)
		}
	}
	if _, err := New(Config{HooksDir: root, Interpreter: InterpreterPowerShell, PowerShellPath: filepath.Join(root, "missing")}); err == nil {
		t.Fatal("expected nonexistent PowerShell executable to fail")
	}
}

func TestPowerShellCommandUsesFixedArgumentsAndRequiresPS1(t *testing.T) {
	root := t.TempDir()
	interpreterName := "pwsh"
	if runtime.GOOS == "windows" {
		interpreterName += ".exe"
	}
	interpreter := filepath.Join(root, interpreterName)
	writeHook(t, root, interpreterName, "#!/bin/sh\nexit 0\n")
	writeHook(t, root, "github/deploy.ps1", "Write-Output 'ok'\n")
	writeHook(t, root, "github/deploy.sh", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(root, "github/deploy.ps1"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{HooksDir: root, Interpreter: InterpreterPowerShell, PowerShellPath: interpreter})
	if err != nil {
		t.Fatal(err)
	}
	path, err := engine.hookPath("github/deploy.ps1")
	if err != nil {
		t.Fatal(err)
	}
	command := engine.hookCommand(context.Background(), path)
	want := []string{engine.config.PowerShellPath, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", path}
	if strings.Join(command.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected PowerShell command: %q", command.Args)
	}
	if _, err := engine.hookPath("github/deploy.sh"); err == nil {
		t.Fatal("expected non-ps1 hook to be rejected")
	}
}

func TestPowerShellHookRunsInOutcomeAndBufferedModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix test interpreter uses /bin/sh")
	}
	root := t.TempDir()
	interpreter := filepath.Join(root, "pwsh")
	writeHook(t, root, "pwsh", "#!/bin/sh\n[ \"$1\" = '-NoLogo' ] && [ \"$2\" = '-NoProfile' ] && [ \"$3\" = '-NonInteractive' ] && [ \"$4\" = '-File' ] || exit 1\nread input\n[ -n \"$input\" ] || exit 1\ncase \"$5\" in\n  *outcome.ps1) printf '{\"response\":{\"status\":202,\"body\":\"accepted\"}}' ;;\n  *buffered.ps1) printf 'streamed' ;;\n  *) exit 1 ;;\nesac\n")
	writeHook(t, root, "outcome.ps1", "# mock PowerShell hook\n")
	writeHook(t, root, "buffered.ps1", "# mock PowerShell hook\n")
	for _, name := range []string{"outcome.ps1", "buffered.ps1"} {
		if err := os.Chmod(filepath.Join(root, name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	structured, err := New(Config{HooksDir: root, Interpreter: InterpreterPowerShell, PowerShellPath: interpreter})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := structured.Execute(context.Background(), "outcome.ps1", automation.Input{})
	if err != nil || outcome.Response == nil || outcome.Response.Status != 202 {
		t.Fatalf("unexpected PowerShell outcome: %#v, %v", outcome, err)
	}
	buffered, err := New(Config{HooksDir: root, Interpreter: InterpreterPowerShell, PowerShellPath: interpreter, OutputMode: ModeBuffered})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := buffered.OpenStream(context.Background(), "buffered.ps1", automation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	var output string
	for chunk := range stream.Chunks {
		output += string(chunk)
	}
	if err := <-stream.Done; err != nil || output != "streamed" {
		t.Fatalf("unexpected PowerShell stream: %q, %v", output, err)
	}
}

func TestPowerShellHookWithInstalledPwsh(t *testing.T) {
	interpreter, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh is not installed")
	}
	interpreter, err = filepath.Abs(interpreter)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeHook(t, root, "respond.ps1", "$request = [Console]::In.ReadToEnd() | ConvertFrom-Json\nif ($request.event.kind -ne 'test') { exit 1 }\n[Console]::Out.Write('{\"response\":{\"status\":202,\"body\":\"accepted\"}}')\n")
	engine, err := New(Config{HooksDir: root, Interpreter: InterpreterPowerShell, PowerShellPath: interpreter})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := engine.Execute(context.Background(), "respond.ps1", automation.Input{Event: map[string]any{"kind": "test"}})
	if err != nil || outcome.Response == nil || outcome.Response.Status != 202 || outcome.Response.Body != "accepted" {
		t.Fatalf("unexpected real PowerShell outcome: %#v, %v", outcome, err)
	}

	writeHook(t, root, "stream.ps1", "[Console]::Out.Write('started')\n[Console]::Out.Flush()\nStart-Sleep -Milliseconds 100\n[Console]::Out.Write('finished')\n")
	buffered, err := New(Config{HooksDir: root, Interpreter: InterpreterPowerShell, PowerShellPath: interpreter, OutputMode: ModeBuffered})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := buffered.OpenStream(context.Background(), "stream.ps1", automation.Input{})
	if err != nil {
		t.Fatal(err)
	}
	var output string
	for chunk := range stream.Chunks {
		output += string(chunk)
	}
	if err := <-stream.Done; err != nil || output != "startedfinished" {
		t.Fatalf("unexpected real PowerShell stream: %q, %v", output, err)
	}

	writeHook(t, root, "slow.ps1", "Start-Sleep -Seconds 5\n")
	timed, err := New(Config{HooksDir: root, Interpreter: InterpreterPowerShell, PowerShellPath: interpreter, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := timed.Execute(context.Background(), "slow.ps1", automation.Input{}); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected real PowerShell hook timeout, got %v", err)
	}
}
