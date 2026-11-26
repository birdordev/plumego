//go:build !linux && !darwin

package main

import (
	"context"
	"errors"

	"with-cloud-manager/internal/kvm"
)

func stageKVMImage(context.Context, string, string, kvm.VM, func() error) error {
	return errors.New("KVM image file staging requires a supported Unix filesystem")
}

func verifyKVMImageVolume(context.Context, string, kvm.VM) error {
	return errors.New("KVM image file verification requires a supported Unix filesystem")
}
