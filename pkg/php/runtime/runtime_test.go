package runtime

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/webong/gateway/arranger/automation"
)

func TestExecuteUsesOptionalPHPCLI(t *testing.T) {
	binary, err := exec.LookPath("php")
	if err != nil {
		t.Skip("PHP CLI is not installed")
	}
	engine := New(WithBinary(binary))
	outcome, err := engine.Execute(context.Background(), `
$gateway->set('ticket.id', 'ticket-1');
$gateway->forward(['url' => 'https://example.test/ticket-1', 'method' => 'POST']);
$gateway->respond(['accepted' => true], 202);
`, automation.Input{Variables: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Response == nil || outcome.Response.Status != 202 || outcome.Response.Body != `{"accepted":true}` {
		t.Fatalf("unexpected response: %+v", outcome.Response)
	}
	if len(outcome.Deliveries) != 1 || outcome.Deliveries[0].URL != "https://example.test/ticket-1" {
		t.Fatalf("unexpected deliveries: %+v", outcome.Deliveries)
	}
	ticket, ok := outcome.Variables["ticket"].(map[string]any)
	if !ok || ticket["id"] != "ticket-1" {
		t.Fatalf("unexpected variables: %+v", outcome.Variables)
	}
}

func TestExecuteHonorsDeadline(t *testing.T) {
	binary, err := exec.LookPath("php")
	if err != nil {
		t.Skip("PHP CLI is not installed")
	}
	engine := New(WithBinary(binary), WithTimeout(50*time.Millisecond))
	_, err = engine.Execute(context.Background(), `sleep(2);`, automation.Input{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}

func TestExecuteBoundsChildDiagnostics(t *testing.T) {
	binary, err := exec.LookPath("php")
	if err != nil {
		t.Skip("PHP CLI is not installed")
	}
	engine := New(WithBinary(binary))
	_, err = engine.Execute(context.Background(), `fwrite(STDERR, str_repeat('x', 20000)); throw new Exception('stop');`, automation.Input{})
	if err == nil {
		t.Fatal("expected PHP automation failure")
	}
	if len(err.Error()) > 4600 {
		t.Fatalf("child diagnostic was not bounded: %d bytes", len(err.Error()))
	}
}
