//go:build darwin || linux

package deliveryqueue

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

func TestSpoolLockChild(t *testing.T) {
	path := os.Getenv("GATEWAY_SPOOL_LOCK_TEST_PATH")
	if path == "" {
		return
	}
	config := testConfig(path, bridge.GatewayDeliveryAdapterFunc(func(ctx context.Context, _ bridge.GatewayDelivery) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	config.DeliveryTimeout = time.Hour
	queue, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close(context.Background())
	id, err := queue.EnqueueGatewayWithID(testDelivery())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("spool-ready:" + id)
	var input [1]byte
	_, _ = os.Stdin.Read(input[:])
}

func TestSpoolLockFencesOtherProcessAndReleasesAfterCrash(t *testing.T) {
	path := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSpoolLockChild$")
	command.Env = append(os.Environ(), "GATEWAY_SPOOL_LOCK_TEST_PATH="+path)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.HasPrefix(strings.TrimSpace(line), "spool-ready:") {
		t.Fatalf("child did not acquire spool: %q, %v", line, err)
	}
	id := strings.TrimPrefix(strings.TrimSpace(line), "spool-ready:")
	if _, err := New(Config{Path: path}); !errors.Is(err, ErrSpoolInUse) {
		t.Fatalf("second process was not fenced: %v", err)
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	queue, err := New(testConfig(path, bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error { return nil })))
	if err != nil {
		t.Fatalf("spool could not be adopted after owner crash: %v", err)
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		status, err := queue.Status(id)
		if err == nil && status.State == StateDelivered {
			break
		}
	}
	if status, err := queue.Status(id); err != nil || status.State != StateDelivered {
		t.Fatalf("adopted spool did not deliver pending item: %+v, %v", status, err)
	}
	if err := queue.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSpoolLockRejectsSymlink(t *testing.T) {
	path := t.TempDir()
	if err := os.Symlink(filepath.Join(t.TempDir(), "lock"), filepath.Join(path, "spool.lock")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := New(Config{Path: path}); err == nil {
		t.Fatal("symlinked spool lock was accepted")
	}
}
