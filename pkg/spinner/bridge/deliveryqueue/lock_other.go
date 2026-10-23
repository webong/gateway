//go:build !darwin && !linux

package deliveryqueue

import (
	"errors"
	"os"
)

func acquireSpoolLock(string) (*os.File, error) {
	return nil, errors.New("delivery spool requires a supported process lock on Linux or macOS")
}

func releaseSpoolLock(*os.File) error { return nil }
