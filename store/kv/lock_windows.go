package kvstore

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func openDirectoryLock(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// A zero share mode denies other opens and deletion until the handle closes.
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		const sharingViolation = syscall.Errno(32)
		if errors.Is(err, sharingViolation) {
			err = fmt.Errorf("directory already open: %w", err)
		}
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}
