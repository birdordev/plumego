//go:build !linux && !darwin

package kvmapp

import (
	"context"
	"errors"
	"io"
	"os"

	"with-cloud-manager/internal/kvm"
)

func StageKVMImage(context.Context, string, string, kvm.VM, func() error) error {
	return errors.New("KVM image file staging requires a supported Unix filesystem")
}

func VerifyKVMImageVolume(context.Context, string, kvm.VM) error {
	return errors.New("KVM image file verification requires a supported Unix filesystem")
}

func publishKVMImage(context.Context, string, string, kvm.BootImageSpec, func() error, func() (io.ReadCloser, error)) error {
	return errors.New("KVM image publication requires a supported Unix filesystem")
}

func PublishKVMBackup(context.Context, string, string, string, string, int64, func() error) (kvm.BootImageSpec, error) {
	return kvm.BootImageSpec{}, errors.New("KVM VM backup requires a supported Unix filesystem")
}

func kvmCleanupPoolSHA256(string, string) (string, error) {
	return "", ErrKVMUnsafeTarget
}

func kvmCleanupFileInfo(string, string) (os.FileInfo, bool, error) {
	return nil, false, ErrKVMUnsafeTarget
}

func hashKVMCleanupFile(context.Context, string, string, kvm.BootImageSpec) (os.FileInfo, error) {
	return nil, ErrKVMUnsafeTarget
}

func checkKVMCleanupFile(string, string, os.FileInfo) error { return ErrKVMUnsafeTarget }

func distinctKVMCleanupDirectories(string, string) bool { return false }
