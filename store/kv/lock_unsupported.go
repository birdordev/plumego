//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !illumos

package kvstore

import (
	"fmt"
	"os"
	"runtime"
)

func openDirectoryLock(_ string) (*os.File, error) {
	return nil, fmt.Errorf("exclusive directory locking is unsupported on %s", runtime.GOOS)
}
