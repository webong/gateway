package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	arranger "github.com/webong/gateway/arranger/integration"
)

func TestMissingBundledRuntimeAdvertisesNothing(t *testing.T) {
	s := &Service{root: t.TempDir()}
	result, err := s.Start(context.Background(), arranger.RuntimeStartRequest{Configuration: json.RawMessage(`{"schema_version":1,"database_ref":"database","authentication_ref":"auth"}`)}, nil)
	if err == nil || len(result.HTTP) != 0 || len(result.PrivateHTTP) != 0 {
		t.Fatalf("missing runtime did not fail closed: %v %+v", err, result)
	}
}
func TestClientCannotChooseExecutableOrBootstrap(t *testing.T) {
	s := &Service{root: t.TempDir()}
	_, err := s.Start(context.Background(), arranger.RuntimeStartRequest{Configuration: json.RawMessage(`{"php":"/usr/bin/php","bootstrap":"/tmp/untrusted.php"}`)}, nil)
	if err == nil {
		t.Fatal("untrusted executable configuration accepted")
	}
}
func TestTerminalChildStopsSiblingsAndRejectsNewWork(t *testing.T) {
	s := &Service{stopped: make(chan struct{})}
	defer s.close()
	helper := func(mode string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestGateChildHelper")
		cmd.Env = append(os.Environ(), "GATE_HELPER="+mode)
		return cmd
	}
	held, err := s.launch(helper("hold"))
	if err != nil {
		t.Fatal(err)
	}
	failed, err := s.launch(helper("exit"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-failed.done:
	case <-time.After(3 * time.Second):
		t.Fatal("child did not exit")
	}
	select {
	case <-s.stopped:
	case <-time.After(8 * time.Second):
		t.Fatal("sibling was not stopped")
	}
	select {
	case <-held.done:
	default:
		t.Fatal("sibling survived")
	}
	if _, err := s.launch(helper("hold")); err == nil {
		t.Fatal("closed runtime launched a new worker")
	}
}
func TestGateChildHelper(t *testing.T) {
	switch os.Getenv("GATE_HELPER") {
	case "exit":
		os.Exit(1)
	case "hold":
		for {
			time.Sleep(time.Hour)
		}
	}
}
