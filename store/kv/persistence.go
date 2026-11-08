package kvstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

const stateFileName = "store.json"

type diskState struct {
	Entries map[string]entry `json:"entries"`
}

func (kv *KVStore) load() error {
	path := filepath.Join(kv.opts.DataDir, stateFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}

	var state diskState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("%w: decode state: %w", ErrCorruptState, err)
	}
	for key, item := range state.Entries {
		if err := validateKey(key); err != nil {
			return fmt.Errorf("%w: decode state key %q: %w", ErrCorruptState, key, err)
		}
		itemCopy := item
		itemCopy.Value = cloneBytes(item.Value)
		itemCopy.Size = entrySize(key, item.Value)
		kv.data[key] = &itemCopy
	}
	return nil
}

// makeDiskStateLocked creates an in-memory snapshot of the current data.
// Must be called with kv.mu held (read or write).
func (kv *KVStore) makeDiskStateLocked() diskState {
	state := diskState{
		Entries: make(map[string]entry, len(kv.data)),
	}
	for key, item := range kv.data {
		state.Entries[key] = entry{
			Value:     cloneBytes(item.Value),
			ExpireAt:  item.ExpireAt,
			UpdatedAt: item.UpdatedAt,
			Size:      item.Size,
		}
	}
	return state
}

// persistState atomically replaces the backing file. The bool reports whether
// replacement succeeded, even if the subsequent directory sync fails.
// Callers must serialize writes through the same store.
func (kv *KVStore) persistState(state diskState) (bool, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return false, fmt.Errorf("encode state: %w", err)
	}

	path := filepath.Join(kv.opts.DataDir, stateFileName)
	tmp, err := os.CreateTemp(kv.opts.DataDir, stateFileName+".*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temp state: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("write temp state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("sync temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return false, fmt.Errorf("replace state: %w", err)
	}
	committed = true
	syncDirectory := kv.syncStateDir
	if syncDirectory == nil {
		syncDirectory = syncDir
	}
	if err := syncDirectory(kv.opts.DataDir); err != nil {
		return true, fmt.Errorf("sync state dir: %w", err)
	}
	return true, nil
}

// persistLocked performs a full persist while holding kv.mu.
// The write lock must remain held through replacement and state reconciliation.
func (kv *KVStore) persistLocked() (bool, error) {
	state := kv.makeDiskStateLocked()
	return kv.persistState(state)
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		if isUnsupportedDirSync(err) {
			return nil
		}
		return err
	}
	return nil
}

func isUnsupportedDirSync(err error) bool {
	return errors.Is(err, os.ErrInvalid) || errors.Is(err, syscall.EINVAL)
}

func (kv *KVStore) cloneDataLocked() map[string]*entry {
	cloned := make(map[string]*entry, len(kv.data))
	for key, item := range kv.data {
		itemCopy := *item
		itemCopy.Value = cloneBytes(item.Value)
		cloned[key] = &itemCopy
	}
	return cloned
}
