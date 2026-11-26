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
	"syscall"

	"with-cloud-manager/internal/kvm"
)

const kvmImageSpool = ".cmp-image-stage"

func openKVMImageRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errKVMUnsafeTarget
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil || actual != path {
		return nil, errKVMUnsafeTarget
	}
	return os.OpenRoot(path)
}

func openKVMImageFile(root *os.Root, name string) (*os.File, os.FileInfo, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, nil, errKVMUnsafeTarget
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, nil, errors.Join(errKVMUnsafeTarget, file.Close())
	}
	return file, after, nil
}

func kvmImageLinkCount(info os.FileInfo) uint64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return uint64(stat.Nlink)
}

type kvmImageReader struct {
	ctx context.Context
	io.Reader
}

func (r kvmImageReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func copyKVMImage(ctx context.Context, dst io.Writer, src io.Reader, vm kvm.VM) error {
	digest := sha256.New()
	reader := kvmImageReader{ctx: ctx, Reader: io.LimitReader(src, vm.ImageSizeBytes+1)}
	n, err := io.CopyBuffer(io.MultiWriter(dst, digest), reader, make([]byte, 1<<20))
	if err != nil {
		return err
	}
	if n != vm.ImageSizeBytes || hex.EncodeToString(digest.Sum(nil)) != vm.ImageSHA256 {
		return errors.New("KVM boot image size or SHA-256 mismatch")
	}
	return nil
}

func verifyKVMImageVolume(ctx context.Context, pool string, vm kvm.VM) (err error) {
	root, err := openKVMImageRoot(pool)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	file, info, err := openKVMImageFile(root, vm.Volume)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	if info.Size() != vm.ImageSizeBytes || kvmImageLinkCount(info) != 1 {
		return errKVMUnsafeTarget
	}
	return copyKVMImage(ctx, io.Discard, file, vm)
}

func lockKVMImageSpool(root *os.Root, volume string) (*os.File, error) {
	if err := root.Mkdir(kvmImageSpool, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	dir, err := root.Lstat(kvmImageSpool)
	if err != nil || !dir.IsDir() || dir.Mode().Perm() != 0700 {
		return nil, errKVMUnsafeTarget
	}
	name := filepath.Join(kvmImageSpool, volume+".lock")
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) {
		var info os.FileInfo
		file, info, err = openKVMImageFile(root, name)
		if err == nil && (kvmImageLinkCount(info) != 1 || info.Mode().Perm() != 0600) {
			return nil, errors.Join(errKVMUnsafeTarget, file.Close())
		}
	}
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, errors.Join(errors.New("KVM boot volume staging is already in progress"), file.Close())
	}
	return file, nil
}

func stageKVMImage(ctx context.Context, imageRoot, pool string, vm kvm.VM, authorize func() error) (err error) {
	if !validKVMImageSpec(vm) || vm.ImageID == "" {
		return errKVMUnsafeTarget
	}
	root, err := openKVMImageRoot(pool)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	lock, err := lockKVMImageSpool(root, vm.Volume)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()

	temp := filepath.Join(kvmImageSpool, vm.Volume+".part")
	existing, info, err := openKVMImageFile(root, vm.Volume)
	if err == nil {
		if info.Size() != vm.ImageSizeBytes {
			return errors.Join(errKVMUnsafeTarget, existing.Close())
		}
		if err := errors.Join(copyKVMImage(ctx, io.Discard, existing, vm), existing.Close()); err != nil {
			return err
		}
		return sealKVMImageLink(root, info, temp, authorize)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := authorize(); err != nil {
		return err
	}
	return copyKVMImageIntoPool(ctx, root, imageRoot, temp, vm, authorize)
}

func copyKVMImageIntoPool(ctx context.Context, root *os.Root, imageRoot, temp string, vm kvm.VM, authorize func() error) (err error) {
	sourceRoot, err := openKVMImageRoot(imageRoot)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, sourceRoot.Close()) }()
	source, info, err := openKVMImageFile(sourceRoot, vm.ImageSHA256+".raw")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	if info.Size() != vm.ImageSizeBytes {
		return errKVMUnsafeTarget
	}
	prior, err := root.Lstat(temp)
	if err == nil {
		if !prior.Mode().IsRegular() || kvmImageLinkCount(prior) != 1 {
			return errKVMUnsafeTarget
		}
		if err := root.Remove(temp); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dst, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			err = errors.Join(err, root.Remove(temp))
		}
	}()
	if err := copyKVMImage(ctx, dst, source, vm); err != nil {
		return errors.Join(err, dst.Close())
	}
	if err := errors.Join(dst.Sync(), dst.Close()); err != nil {
		return err
	}
	if err := authorize(); err != nil {
		return err
	}
	if err := root.Link(temp, vm.Volume); err != nil {
		return err
	}
	// 链接存在后保留私有别名，直到目标路径持久化。崩溃后可验证并收尾。
	cleanup = false
	published, err := root.Lstat(vm.Volume)
	if err != nil {
		return err
	}
	return sealKVMImageLink(root, published, temp, authorize)
}

func sealKVMImageLink(root *os.Root, info os.FileInfo, temp string, authorize func() error) error {
	switch kvmImageLinkCount(info) {
	case 1:
		return syncKVMImageDirectory(root, ".")
	case 2:
		link, err := root.Lstat(temp)
		if err != nil || !link.Mode().IsRegular() || !os.SameFile(info, link) {
			return errKVMUnsafeTarget
		}
	default:
		return errKVMUnsafeTarget
	}
	if err := syncKVMImageDirectory(root, "."); err != nil {
		return err
	}
	if err := authorize(); err != nil {
		return err
	}
	if err := root.Remove(temp); err != nil {
		return err
	}
	return syncKVMImageDirectory(root, kvmImageSpool)
}

func syncKVMImageDirectory(root *os.Root, name string) error {
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
