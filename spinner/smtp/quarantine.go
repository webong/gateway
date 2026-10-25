package smtp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type QuarantinedMessage struct {
	TransactionID string
	EnvelopeFrom  string
	Recipients    []string
	Reason        string
	Payload       []byte
}

type QuarantineStore interface {
	Store(context.Context, QuarantinedMessage) (string, error)
}

type QuarantinePruner interface {
	PruneBefore(time.Time) (int, error)
}

type QuarantineRecord struct {
	ID            string    `json:"id"`
	TransactionID string    `json:"transaction_id"`
	ReceivedAt    time.Time `json:"received_at"`
	EnvelopeFrom  string    `json:"envelope_from"`
	Recipients    []string  `json:"recipients"`
	Reason        string    `json:"reason"`
	Size          int       `json:"size"`
	SHA256        string    `json:"sha256"`
}

// FileQuarantine retains a blocked message on a restricted, operator-owned
// volume. A single atomic file contains a JSON metadata line and raw RFC 5322
// bytes; no SMTP success is returned until that file and its directory sync.
type FileQuarantine struct{ path string }

func NewFileQuarantine(path string) (*FileQuarantine, error) {
	return openFileQuarantine(path, true)
}

// OpenFileQuarantine inspects an existing store without creating one on a
// mistyped path. It is intended for local operator tooling, not public HTTP.
func OpenFileQuarantine(path string) (*FileQuarantine, error) {
	return openFileQuarantine(path, false)
}

func openFileQuarantine(path string, create bool) (*FileQuarantine, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return nil, errors.New("quarantine path must be an absolute dedicated directory")
	}
	path = filepath.Clean(path)
	if create {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("create quarantine directory: %w", err)
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("quarantine directory must be private and not a symlink")
	}
	return &FileQuarantine{path: path}, nil
}

func validQuarantineID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, character := range id {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}

func (quarantine *FileQuarantine) open(id string) (*os.File, error) {
	if quarantine == nil || !validQuarantineID(id) {
		return nil, errors.New("invalid quarantine ID")
	}
	path := filepath.Join(quarantine.path, id+".quarantine")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("quarantine item is not a private regular file")
	}
	return os.Open(path)
}

func readQuarantineHeader(reader *bufio.Reader, id string) (QuarantineRecord, error) {
	header, err := reader.ReadSlice('\n')
	if err != nil {
		return QuarantineRecord{}, fmt.Errorf("read quarantine metadata: %w", err)
	}
	var record QuarantineRecord
	if err := json.Unmarshal(header, &record); err != nil || record.ID != id || record.Size < 0 {
		return QuarantineRecord{}, errors.New("invalid quarantine metadata")
	}
	return record, nil
}

func (quarantine *FileQuarantine) List() ([]QuarantineRecord, error) {
	if quarantine == nil {
		return nil, errors.New("quarantine storage is not configured")
	}
	entries, err := os.ReadDir(quarantine.path)
	if err != nil {
		return nil, err
	}
	records := make([]QuarantineRecord, 0)
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".quarantine")
		if !strings.HasSuffix(entry.Name(), ".quarantine") || !validQuarantineID(id) {
			continue
		}
		file, err := quarantine.open(id)
		if err != nil {
			return nil, err
		}
		record, readErr := readQuarantineHeader(bufio.NewReaderSize(file, 64*1024), id)
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		records = append(records, record)
	}
	sort.Slice(records, func(left, right int) bool { return records[left].ReceivedAt.After(records[right].ReceivedAt) })
	return records, nil
}

func (quarantine *FileQuarantine) Read(id string) (QuarantineRecord, []byte, error) {
	file, err := quarantine.open(id)
	if err != nil {
		return QuarantineRecord{}, nil, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64*1024)
	record, err := readQuarantineHeader(reader, id)
	if err != nil {
		return QuarantineRecord{}, nil, err
	}
	if record.Size > 50*1024*1024 {
		return QuarantineRecord{}, nil, errors.New("quarantine item exceeds the inspection limit")
	}
	payload, err := io.ReadAll(io.LimitReader(reader, int64(record.Size)+1))
	if err != nil || len(payload) != record.Size {
		return QuarantineRecord{}, nil, errors.New("quarantine payload size does not match metadata")
	}
	digest := sha256.Sum256(payload)
	if record.SHA256 != hex.EncodeToString(digest[:]) {
		return QuarantineRecord{}, nil, errors.New("quarantine payload digest does not match metadata")
	}
	return record, payload, nil
}

func (quarantine *FileQuarantine) Delete(id string) error {
	file, err := quarantine.open(id)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(quarantine.path, id+".quarantine")); err != nil {
		return err
	}
	directory, err := os.Open(quarantine.path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// PruneBefore removes only private quarantine records with parseable metadata
// older than the cutoff. Unsafe or unreadable entries stop pruning so an
// operator can inspect them instead of silently losing evidence.
func (quarantine *FileQuarantine) PruneBefore(cutoff time.Time) (int, error) {
	records, err := quarantine.List()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, record := range records {
		if record.ReceivedAt.IsZero() || !record.ReceivedAt.Before(cutoff) {
			continue
		}
		if err := quarantine.Delete(record.ID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func (quarantine *FileQuarantine) Store(ctx context.Context, message QuarantinedMessage) (string, error) {
	if quarantine == nil || quarantine.path == "" {
		return "", errors.New("quarantine storage is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return "", err
	}
	id := hex.EncodeToString(identifier)
	digest := sha256.Sum256(message.Payload)
	record := QuarantineRecord{
		ID: id, TransactionID: message.TransactionID, ReceivedAt: time.Now().UTC(),
		EnvelopeFrom: message.EnvelopeFrom, Recipients: append([]string(nil), message.Recipients...),
		Reason: message.Reason, Size: len(message.Payload), SHA256: hex.EncodeToString(digest[:]),
	}
	header, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	tempPath := filepath.Join(quarantine.path, "."+id+".tmp")
	finalPath := filepath.Join(quarantine.path, id+".quarantine")
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer os.Remove(tempPath)
	if _, err := file.Write(append(header, '\n')); err != nil {
		file.Close()
		return "", err
	}
	if _, err := io.Copy(file, bytes.NewReader(message.Payload)); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return "", err
	}
	directory, err := os.Open(quarantine.path)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return "", err
	}
	return id, nil
}

var _ QuarantineStore = (*FileQuarantine)(nil)
var _ QuarantinePruner = (*FileQuarantine)(nil)
