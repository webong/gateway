package smtp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileQuarantinePersistsOriginalBytesPrivately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quarantine")
	quarantine, err := NewFileQuarantine(path)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("Subject: test\r\n\r\noriginal\x00bytes\r\n")
	id, err := quarantine.Store(context.Background(), QuarantinedMessage{
		TransactionID: "transaction-1", EnvelopeFrom: "sender@example.test",
		Recipients: []string{"inbox@example.test"}, Reason: "Eicar-Test-Signature", Payload: message,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 {
		t.Fatalf("unexpected quarantine ID %q", id)
	}
	stored, err := os.ReadFile(filepath.Join(path, id+".quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	header, payload, found := bytes.Cut(stored, []byte("\n"))
	if !found || !bytes.Equal(payload, message) {
		t.Fatal("quarantine lost original RFC 5322 bytes")
	}
	var record QuarantineRecord
	if err := json.Unmarshal(header, &record); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(message)
	if record.ID != id || record.TransactionID != "transaction-1" || record.Size != len(message) || record.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("unexpected quarantine metadata: %+v", record)
	}
	info, err := os.Stat(filepath.Join(path, id+".quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("quarantine file mode: %v", info.Mode())
	}
	records, err := quarantine.List()
	if err != nil || len(records) != 1 || records[0].ID != id {
		t.Fatalf("quarantine listing failed: %+v, %v", records, err)
	}
	readRecord, readPayload, err := quarantine.Read(id)
	if err != nil || readRecord.ID != id || !bytes.Equal(readPayload, message) {
		t.Fatalf("quarantine inspection failed: %+v, %v", readRecord, err)
	}
	if _, _, err := quarantine.Read("../unsafe"); err == nil {
		t.Fatal("unsafe quarantine ID was accepted")
	}
	if err := quarantine.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, id+".quarantine")); !os.IsNotExist(err) {
		t.Fatalf("quarantine item was not deleted: %v", err)
	}
}

func TestFileQuarantinePrunesOnlyRecordsBeforeCutoff(t *testing.T) {
	quarantine, err := NewFileQuarantine(filepath.Join(t.TempDir(), "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	oldID, err := quarantine.Store(context.Background(), QuarantinedMessage{Payload: []byte("old")})
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().UTC()
	newID, err := quarantine.Store(context.Background(), QuarantinedMessage{Payload: []byte("new")})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := quarantine.PruneBefore(cutoff)
	if err != nil || removed != 1 {
		t.Fatalf("expected one old record pruned: %d, %v", removed, err)
	}
	if _, _, err := quarantine.Read(oldID); !os.IsNotExist(err) {
		t.Fatalf("old record still exists: %v", err)
	}
	if _, _, err := quarantine.Read(newID); err != nil {
		t.Fatalf("new record was pruned: %v", err)
	}
}

func TestFileQuarantineRejectsBroadOrSharedPaths(t *testing.T) {
	if _, err := NewFileQuarantine("relative"); err == nil {
		t.Fatal("accepted relative path")
	}
	if _, err := NewFileQuarantine("/"); err == nil {
		t.Fatal("accepted root directory")
	}
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileQuarantine(shared); err == nil {
		t.Fatal("accepted shared directory")
	}
}
