// seed-store prepares only disposable smoke-test caches. Real hosts install
// through their own authenticated desired-state/download paths.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/webong/ext/pkg/ext-go/engine/host/store"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: seed-store CACHE ARCHIVE SHA256 ID")
		os.Exit(2)
	}
	if err := seed(os.Args[1], os.Args[2], os.Args[3], os.Args[4]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func seed(root, archive, digest, id string) error {
	packages, err := store.New(root, store.Limits{MaxArchiveBytes: 512 << 20, MaxArtifactBytes: 512 << 20, MaxTreeBytes: 512 << 20, MaxTreeFiles: 250000})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// These fixture binaries/helpers are built locally, not client uploads.
	// Preserve the old fixture's executable modes for cross-UID Docker mounts.
	_, err = packages.InstallArchive(ctx, store.Archive{Source: archive, SHA256: digest, ID: id}, store.ArchiveOptions{Copy: store.CopyOptions{PreserveExecutableArtifacts: true}})
	if err != nil {
		return err
	}
	if _, err := packages.Receipt(id); err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if entry.IsDir() || info.Mode()&0100 != 0 {
			mode = 0755
		}
		return os.Chmod(path, mode)
	})
}
