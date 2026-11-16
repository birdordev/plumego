package kvstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"
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
	if !utf8.Valid(raw) {
		return fmt.Errorf("%w: snapshot must be valid UTF-8", ErrCorruptState)
	}

	var state struct {
		Entries map[string]*entry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("%w: decode state: %w", ErrCorruptState, err)
	}
	if state.Entries == nil {
		return fmt.Errorf("%w: state requires an entries object", ErrCorruptState)
	}
	if err := checkSnapshotObject(json.NewDecoder(bytes.NewReader(raw)), snapshotRoot); err != nil {
		return fmt.Errorf("%w: %w", ErrCorruptState, err)
	}
	for key, item := range state.Entries {
		if err := validateKey(key); err != nil {
			return fmt.Errorf("%w: decode state key %q: %w", ErrCorruptState, key, err)
		}
		if item == nil {
			return fmt.Errorf("%w: snapshot contains a null entry", ErrCorruptState)
		}
		itemCopy := *item
		itemCopy.Value = cloneBytes(item.Value)
		itemCopy.Size = entrySize(key, item.Value)
		kv.data[key] = &itemCopy
	}
	return nil
}

type snapshotObject uint8

const (
	snapshotRoot snapshotObject = iota
	snapshotEntries
	snapshotEntry
)

// The typed decode already validates JSON syntax and schema types. A second
// pass sees members in order, before duplicate names can be merged or replaced.
func checkSnapshotObject(dec *json.Decoder, object snapshotObject) error {
	start, err := dec.Token()
	if err != nil || start == nil {
		return err
	}
	if start != json.Delim('{') {
		return errors.New("expected snapshot object")
	}
	seen := make(map[string]struct{})
	var value json.RawMessage
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		name := snapshotMemberName(object, token.(string))
		if _, exists := seen[name]; exists {
			return errors.New("snapshot contains a duplicate member")
		}
		seen[name] = struct{}{}
		switch {
		case object == snapshotRoot && name == "entries":
			err = checkSnapshotObject(dec, snapshotEntries)
		case object == snapshotEntries:
			err = checkSnapshotObject(dec, snapshotEntry)
		default:
			err = dec.Decode(&value)
		}
		if err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

// Struct field matching in encoding/json is case-insensitive. Map keys remain
// case-sensitive, including keys that happen to match a struct field name.
func snapshotMemberName(object snapshotObject, name string) string {
	switch object {
	case snapshotRoot:
		if strings.EqualFold(name, "entries") {
			return "entries"
		}
	case snapshotEntry:
		for _, field := range [...]string{"value", "expire_at", "updated_at", "size"} {
			if strings.EqualFold(name, field) {
				return field
			}
		}
	}
	return name
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
	// Published entries are immutable. Mutations only replace or delete map entries.
	return maps.Clone(kv.data)
}
