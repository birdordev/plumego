//go:build !linux

package main

import "errors"

func kvmLocalFreeMiB(string) (int64, error) {
	return 0, errors.New("local KVM capacity collection requires Linux")
}

func kvmSharedFreeMiB(string) (int64, error) {
	return 0, errors.New("KVM shared storage capacity collection requires Linux")
}

func kvmLocalBootFile(string, int64) error {
	return errors.New("local KVM VM execution requires Linux")
}
