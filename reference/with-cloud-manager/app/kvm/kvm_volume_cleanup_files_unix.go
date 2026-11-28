//go:build linux || darwin

package kvmapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"with-cloud-manager/internal/kvm"
)

func kvmCleanupPoolSHA256(path, uuid string) (digest string, err error) {
	root, err := OpenKVMImageRoot(path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	info, err := root.Stat(".")
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return "", ErrKVMUnsafeTarget
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n%d", uuid, path, stat.Dev, stat.Ino)))
	return hex.EncodeToString(hash[:]), nil
}

func kvmCleanupFileInfo(path, name string) (info os.FileInfo, present bool, err error) {
	root, err := OpenKVMImageRoot(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	info, err = root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || KVMImageLinkCount(info) != 1 {
		return nil, false, ErrKVMUnsafeTarget
	}
	return info, true, nil
}

func hashKVMCleanupFile(ctx context.Context, path, name string, image kvm.BootImageSpec) (identity os.FileInfo, err error) {
	root, err := OpenKVMImageRoot(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	file, original, err := openKVMImageFile(root, name, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if original.Size() != image.SizeBytes || KVMImageLinkCount(original) != 1 {
		return nil, ErrKVMUnsafeTarget
	}
	if err := copyKVMImage(ctx, io.Discard, file, image); err != nil {
		return nil, err
	}
	if err := checkKVMCleanupFile(path, name, original); err != nil {
		return nil, err
	}
	return original, nil
}

func checkKVMCleanupFile(path, name string, original os.FileInfo) error {
	current, present, err := kvmCleanupFileInfo(path, name)
	if err != nil {
		return err
	}
	if !present || !os.SameFile(original, current) || original.Size() != current.Size() ||
		!original.ModTime().Equal(current.ModTime()) || original.Mode() != current.Mode() {
		return ErrKVMUnsafeTarget
	}
	return nil
}

func distinctKVMCleanupDirectories(pool, backup string) bool {
	for _, pair := range [][2]string{{pool, backup}, {backup, pool}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err != nil || !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != ".." {
			return false
		}
	}
	source, err := os.Stat(pool)
	if err != nil {
		return false
	}
	destination, err := os.Stat(backup)
	return err == nil && !os.SameFile(source, destination)
}
