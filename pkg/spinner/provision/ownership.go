package provision

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type workerOwnership struct {
	directory string
	id        string
	file      *os.File
}

func (o *workerOwnership) Close() error { return o.file.Close() }

func acquireWorkerOwnership(config Config) (*workerOwnership, error) {
	if config.NodeID == "" || !filepath.IsAbs(config.StateDirectory) {
		return nil, fmt.Errorf("provisioning requires a node ID and absolute persistent state directory")
	}
	key := sha256.Sum256([]byte(config.TargetID + "\x00" + config.NodeID))
	directory := filepath.Join(config.StateDirectory, hex.EncodeToString(key[:16]))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("worker state directory must be private (0700)")
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(directory, "controller.lock")
	if info, err := os.Lstat(lockPath); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("invalid controller lock")
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockWorker(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("another local controller owns this node: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			file.Close()
		}
	}()
	ownerPath := filepath.Join(directory, "owner")
	raw, err := os.ReadFile(ownerPath)
	if os.IsNotExist(err) {
		random := make([]byte, 16)
		if _, err = rand.Read(random); err != nil {
			return nil, err
		}
		raw = []byte(hex.EncodeToString(random))
		owner, err := os.OpenFile(ownerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		_, writeErr := owner.Write(raw)
		syncErr := owner.Sync()
		closeErr := owner.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return nil, fmt.Errorf("persist worker owner: %v %v %v", writeErr, syncErr, closeErr)
		}
	} else if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(string(raw))
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 16 || len(id) != 32 {
		return nil, fmt.Errorf("invalid persistent worker owner; do not reset an active journal")
	}
	failed = false
	return &workerOwnership{directory: directory, id: id, file: file}, nil
}
