package kvengine

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	opSet    byte = 1
	opDelete byte = 2
)

// WALEntry represents a Write-Ahead Log entry
type WALEntry struct {
	Op       byte      `json:"op"`
	Key      string    `json:"key"`
	Value    []byte    `json:"value,omitempty"`
	ExpireAt time.Time `json:"expire_at,omitempty"`
	Version  int64     `json:"version"`
	CRC      uint32    `json:"crc"`
}

// WALSyncMode controls when acknowledged WAL writes are flushed to durable storage.
type WALSyncMode string

const (
	// WALSyncImmediate flushes and fsyncs each WAL entry before acknowledging writes.
	WALSyncImmediate WALSyncMode = "immediate"
	// WALSyncInterval relies on the background flusher and close path for fsync.
	WALSyncInterval WALSyncMode = "interval"
)

// initWAL initializes the Write-Ahead Log
func (kv *KVStore) initWAL() error {
	walPath := filepath.Join(kv.opts.DataDir, "store.wal")

	file, err := os.OpenFile(walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open WAL: %w", err)
	}

	kv.walFile = file
	kv.walWriter = bufio.NewWriter(file)
	return nil
}

// walFlusher handles periodic WAL flushing to disk
func (kv *KVStore) walFlusher() {
	ticker := time.NewTicker(kv.opts.FlushInterval)
	defer ticker.Stop()
	defer kv.wg.Done()

	for {
		select {
		case <-ticker.C:
			_ = kv.flushWAL()
		case <-kv.ctx.Done():
			_ = kv.flushWAL()
			return
		}
	}
}

func (kv *KVStore) writeWALEntry(entry WALEntry) error {
	kv.walMutex.Lock()
	defer kv.walMutex.Unlock()

	data, err := kv.encodeWALEntry(entry)
	if err != nil {
		return err
	}

	n, err := kv.walWriter.Write(data)
	kv.walDirty = kv.walDirty || n > 0
	if err != nil {
		return err
	}
	if kv.opts.WALSyncMode == WALSyncImmediate {
		if err := kv.flushWALLocked(); err != nil {
			return err
		}
	}

	atomic.AddInt64(&kv.walSize, int64(n))
	return nil
}

func (kv *KVStore) flushWAL() error {
	kv.walMutex.Lock()
	defer kv.walMutex.Unlock()
	return kv.flushWALLocked()
}

func (kv *KVStore) flushWALLocked() error {
	if kv.walWriter != nil {
		if err := kv.walWriter.Flush(); err != nil {
			return err
		}
		if kv.walDirty && kv.walFile != nil {
			if err := kv.walFile.Sync(); err != nil {
				return err
			}
		}
		kv.walDirty = false
	}
	return nil
}

func (kv *KVStore) resetWAL() error {
	kv.walMutex.Lock()
	defer kv.walMutex.Unlock()

	walPath := filepath.Join(kv.opts.DataDir, "store.wal")
	if err := kv.flushWALLocked(); err != nil {
		return fmt.Errorf("flush WAL before reset: %w", err)
	}
	if kv.walFile != nil {
		if err := kv.walFile.Close(); err != nil {
			return fmt.Errorf("close old WAL: %w", err)
		}
	}

	file, err := os.OpenFile(walPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("reset WAL: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync reset WAL: %w", err)
	}
	if err := syncDataDir(kv.opts.DataDir); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync WAL directory: %w", err)
	}

	kv.walFile = file
	kv.walWriter = bufio.NewWriter(file)
	atomic.StoreInt64(&kv.walSize, 0)
	return nil
}

func (kv *KVStore) replayWAL() error {
	walPath := filepath.Join(kv.opts.DataDir, "store.wal")

	file, err := os.Open(walPath)
	if os.IsNotExist(err) {
		return nil // No WAL exists
	}
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() == 0 {
		return nil
	}

	// Auto-detect WAL format if enabled
	serializer := kv.serializer
	if kv.opts.AutoDetectMode == AutoDetectEnabled {
		format, detectErr := DetectWALFormat(file)
		if detectErr != nil {
			return detectErr
		}
		serializer = GetSerializer(format)
	}

	reader := bufio.NewReader(file)

	for {
		entry, err := serializer.DecodeWALEntry(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: decode WAL: %w", ErrInvalidEntry, err)
		}

		if !kv.validateWALEntry(*entry) {
			return fmt.Errorf("%w: CRC mismatch for key %q", ErrInvalidEntry, entry.Key)
		}

		switch entry.Op {
		case opSet:
			kv.restoreEntry(&Entry{
				Key:      entry.Key,
				Value:    entry.Value,
				ExpireAt: entry.ExpireAt,
				Version:  entry.Version,
			})

		case opDelete:
			kv.removeRestoredEntry(entry.Key)
		}
	}

	return nil
}

// restoreEntry applies the current cache budget without changing the persisted data.
func (kv *KVStore) restoreEntry(entry *Entry) {
	entry.Size = int64(len(entry.Key)) + int64(len(entry.Value)) + 64
	maxMemory := int64(kv.opts.MaxMemoryMB) * 1024 * 1024
	if entry.Size > maxMemory {
		// A skipped replacement must still invalidate the previous value.
		kv.removeRestoredEntry(entry.Key)
		return
	}
	kv.putEntry(entry, maxMemory)
}

// removeRestoredEntry runs before the store is exposed or its workers start.
func (kv *KVStore) removeRestoredEntry(key string) {
	shard := kv.getShard(key)
	if entry := shard.data[key]; entry != nil {
		kv.deleteFromShard(shard, key, entry)
	}
}

func (kv *KVStore) encodeWALEntry(entry WALEntry) ([]byte, error) {
	return kv.serializer.EncodeWALEntry(entry)
}

func (kv *KVStore) calculateCRC(entry WALEntry) uint32 {
	// The CRC covers the historical decimal/hex representation.
	if len(entry.Value) < 4<<10 {
		data := fmt.Sprintf("%d\x00%s\x00%x\x00%d\x00%d", entry.Op, entry.Key, entry.Value, entry.ExpireAt.UnixNano(), entry.Version)
		return crc32.ChecksumIEEE([]byte(data))
	}
	var numbers [48]byte
	head := append(strconv.AppendUint(numbers[:0], uint64(entry.Op), 10), 0)
	crc := crc32.Update(0, crc32.IEEETable, head)
	crc = crc32.Update(crc, crc32.IEEETable, []byte(entry.Key))
	crc = crc32.Update(crc, crc32.IEEETable, []byte{0})
	var chunk [4096]byte
	for rest := entry.Value; len(rest) != 0; {
		n := min(len(rest), len(chunk)/2)
		encoded := hex.Encode(chunk[:], rest[:n])
		crc = crc32.Update(crc, crc32.IEEETable, chunk[:encoded])
		rest = rest[n:]
	}
	tail := strconv.AppendInt(append(numbers[:0], 0), entry.ExpireAt.UnixNano(), 10)
	tail = strconv.AppendInt(append(tail, 0), entry.Version, 10)
	return crc32.Update(crc, crc32.IEEETable, tail)
}

func (kv *KVStore) validateWALEntry(entry WALEntry) bool {
	expected := kv.calculateCRC(entry)
	return entry.CRC == expected
}

func (kv *KVStore) closeWAL() error {
	if kv.walFile == nil {
		return nil
	}
	var err error
	if flushErr := kv.flushWAL(); flushErr != nil {
		err = errors.Join(err, flushErr)
	}
	if closeErr := kv.walFile.Close(); closeErr != nil {
		err = errors.Join(err, closeErr)
	}
	return err
}
