//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || illumos

package kvstore

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func openDirectoryLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			err = fmt.Errorf("directory already open: %w", err)
		}
		return nil, errors.Join(err, file.Close())
	}
	return file, nil
}
