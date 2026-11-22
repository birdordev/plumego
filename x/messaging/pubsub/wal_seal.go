package pubsub

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

const walSealSize = 28

// writeWALSeal stores an optional hint for a finalized segment. Recovery falls
// back to replay if the hint is missing, stale or incomplete.
func writeWALSeal(path string, info os.FileInfo, last uint64) {
	var header [walSealSize]byte
	binary.LittleEndian.PutUint64(header[0:], last)
	binary.LittleEndian.PutUint64(header[8:], uint64(info.Size()))
	binary.LittleEndian.PutUint64(header[16:], uint64(info.ModTime().UnixNano()))
	binary.LittleEndian.PutUint32(header[24:], crc32.ChecksumIEEE(header[:24]))

	f, err := os.CreateTemp(filepath.Dir(path), ".wal-range-*")
	if err != nil {
		return
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	_, writeErr := f.Write(header[:])
	closeErr := f.Close()
	if writeErr == nil && closeErr == nil {
		_ = os.Rename(name, path+".range")
	}
}

func sealedWALBefore(path string, minimum uint64) bool {
	f, err := os.Open(path + ".range")
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var header [walSealSize + 1]byte
	n, _ := io.ReadFull(f, header[:])
	if n != walSealSize || binary.LittleEndian.Uint32(header[24:]) != crc32.ChecksumIEEE(header[:24]) {
		return false
	}
	if binary.LittleEndian.Uint64(header[:]) >= minimum {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() &&
		uint64(info.Size()) == binary.LittleEndian.Uint64(header[8:]) &&
		uint64(info.ModTime().UnixNano()) == binary.LittleEndian.Uint64(header[16:])
}
