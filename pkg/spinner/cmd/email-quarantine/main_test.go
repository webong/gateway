package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/webong/gateway/pkg/spinner/bridge/smtp"
)

func TestOperatorExportRequiresNewFileAndExplicitDeleteID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quarantine")
	quarantine, err := smtp.NewFileQuarantine(path)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("Subject: review\r\n\r\noriginal\r\n")
	id, err := quarantine.Store(context.Background(), smtp.QuarantinedMessage{Payload: message})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "review.eml")
	if err := run([]string{"export", "-path", path, "-id", id, "-output", output}); err != nil {
		t.Fatal(err)
	}
	exported, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(exported, message) {
		t.Fatalf("unexpected export: %v", err)
	}
	if err := run([]string{"export", "-path", path, "-id", id, "-output", output}); err == nil {
		t.Fatal("export overwrote an existing file")
	}
	if err := run([]string{"delete", "-path", path, "-id", "../unsafe"}); err == nil {
		t.Fatal("unsafe delete ID was accepted")
	}
	if err := run([]string{"delete", "-path", path, "-id", id}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := quarantine.Read(id); !os.IsNotExist(err) {
		t.Fatalf("quarantine item was not deleted: %v", err)
	}
}
