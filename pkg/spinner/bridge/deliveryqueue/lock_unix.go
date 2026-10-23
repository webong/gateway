//go:build darwin || linux

package deliveryqueue

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func acquireSpoolLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open delivery spool lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil {
		file.Close()
		return nil, fmt.Errorf("inspect delivery spool lock: %w", err)
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG {
		file.Close()
		return nil, errors.New("delivery spool lock must be a regular file")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrSpoolInUse
		}
		return nil, fmt.Errorf("lock delivery spool: %w", err)
	}
	return file, nil
}

func releaseSpoolLock(file *os.File) error {
	if file == nil {
		return nil
	}
	err := unix.Flock(int(file.Fd()), unix.LOCK_UN)
	return errors.Join(err, file.Close())
}
