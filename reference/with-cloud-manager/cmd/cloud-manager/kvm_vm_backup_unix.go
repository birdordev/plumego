//go:build linux || darwin

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"with-cloud-manager/internal/kvm"
)

func publishKVMBackup(ctx context.Context, pool, directory, volume, imageID string, size int64, authorize func() error) (image kvm.BootImageSpec, err error) {
	if size < 1 || size > kvm.MaxBootImageBytes || pool == directory ||
		!kvmUnitName.MatchString(volume) || !kvmUnitName.MatchString(imageID) {
		return image, errKVMUnsafeTarget
	}
	root, err := openKVMImageRoot(pool)
	if err != nil {
		return image, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	sourceDirectory, err := root.Stat(".")
	if err != nil {
		return image, err
	}
	destinationDirectory, err := os.Lstat(directory)
	if err != nil || !destinationDirectory.IsDir() || os.SameFile(sourceDirectory, destinationDirectory) {
		return image, errKVMUnsafeTarget
	}
	file, original, err := openKVMImageFile(root, volume, os.O_RDONLY)
	if err != nil {
		return image, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	verify := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := authorize(); err != nil {
			return err
		}
		currentDirectory, err := os.Lstat(pool)
		if err != nil || !currentDirectory.IsDir() || !os.SameFile(sourceDirectory, currentDirectory) {
			return errKVMUnsafeTarget
		}
		current, err := root.Lstat(volume)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(original, current) ||
			current.Size() != size || current.ModTime() != original.ModTime() || kvmImageLinkCount(current) != 1 {
			return errKVMUnsafeTarget
		}
		return nil
	}
	if err := verify(); err != nil {
		return image, err
	}
	hasher := sha256.New()
	bounded := kvmImageReader{ctx: ctx, Reader: io.LimitReader(file, size+1)}
	n, err := io.CopyBuffer(hasher, bounded, make([]byte, 1<<20))
	if err != nil {
		return image, err
	}
	if n != size {
		return image, errKVMImageMismatch
	}
	image = kvm.BootImageSpec{ID: imageID, SHA256: hex.EncodeToString(hasher.Sum(nil)), SizeBytes: size}
	if err := verify(); err != nil {
		return kvm.BootImageSpec{}, err
	}
	// 发布时通过第二次读取验证内容仍与已计算的摘要一致。
	name := image.SHA256 + ".raw"
	err = publishKVMImage(ctx, directory, name, image, verify, func() (io.ReadCloser, error) {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return io.NopCloser(file), nil
	})
	if err != nil {
		return kvm.BootImageSpec{}, err
	}
	published, err := os.Lstat(filepath.Join(directory, name))
	if err != nil || !published.Mode().IsRegular() || published.Mode().Perm() != 0600 {
		return kvm.BootImageSpec{}, errKVMUnsafeTarget
	}
	currentDirectory, err := os.Lstat(directory)
	if err != nil || !os.SameFile(destinationDirectory, currentDirectory) {
		return kvm.BootImageSpec{}, errKVMUnsafeTarget
	}
	if err := verify(); err != nil {
		return kvm.BootImageSpec{}, err
	}
	return image, nil
}
