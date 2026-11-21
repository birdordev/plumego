package kvstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

const stateFileName = "store.json"

type diskState struct {
	Entries map[string]*entry `json:"entries"`
}

func (kv *KVStore) load() error {
	path := filepath.Join(kv.opts.DataDir, stateFileName)
	raw, err := readSnapshot(path, kv.snapshotReadLimit())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if !utf8.Valid(raw) {
		return fmt.Errorf("%w: snapshot must be valid UTF-8", ErrCorruptState)
	}

	var state diskState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("%w: decode state: %w", ErrCorruptState, err)
	}
	if state.Entries == nil {
		return fmt.Errorf("%w: state requires an entries object", ErrCorruptState)
	}
	if err := checkSnapshotObject(json.NewDecoder(bytes.NewReader(raw)), raw, snapshotRoot); err != nil {
		return fmt.Errorf("%w: %w", ErrCorruptState, err)
	}
	for key, item := range state.Entries {
		if err := validateKey(key); err != nil {
			return fmt.Errorf("%w: decode state key %q: %w", ErrCorruptState, key, err)
		}
		if item == nil {
			return fmt.Errorf("%w: snapshot contains a null entry", ErrCorruptState)
		}
		// The decoded entry and its value are owned by this load.
		item.Size = entrySize(key, item.Value)
		kv.data[key] = item
	}
	return nil
}

// Keep enough room for snapshots written with the former default capacities
// even when an opener uses smaller limits. JSON string escaping can expand
// key bytes up to sixfold; the entry size accounting covers field overhead.
func (kv *KVStore) snapshotReadLimit() int64 {
	budget := max(kv.maxMemoryBytes(), defaultMaxMemoryMB*bytesPerMiB)
	if budget > (math.MaxInt64-1)/6 {
		return math.MaxInt64 - 1
	}
	return budget * 6
}

func readSnapshot(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("snapshot exceeds the %d-byte input budget", limit)
	}

	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("snapshot exceeds the %d-byte input budget", limit)
	}
	return raw, nil
}

type snapshotObject uint8

const (
	snapshotRoot snapshotObject = iota
	snapshotEntries
	snapshotEntry
)

// The typed decode already validates JSON syntax and schema types. A second
// pass sees member order and raw key spellings that the typed decode loses.
func checkSnapshotObject(dec *json.Decoder, raw []byte, object snapshotObject) error {
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
		offset := dec.InputOffset()
		token, err := dec.Token()
		if err != nil {
			return err
		}
		if object == snapshotEntries {
			key := bytes.TrimLeft(raw[offset:dec.InputOffset()], ", \t\r\n")
			if !snapshotKeyHasPairedSurrogates(key) {
				return errors.New("snapshot key contains an unpaired Unicode surrogate")
			}
		}
		name := snapshotMemberName(object, token.(string))
		if _, exists := seen[name]; exists {
			return errors.New("snapshot contains a duplicate member")
		}
		seen[name] = struct{}{}
		switch {
		case object == snapshotRoot && name == "entries":
			err = checkSnapshotObject(dec, raw, snapshotEntries)
		case object == snapshotEntries:
			err = checkSnapshotObject(dec, raw, snapshotEntry)
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

// key is a syntactically valid JSON string, including quotes. The JSON decoder
// replaces unpaired surrogates, so inspect the escapes before using its result.
func snapshotKeyHasPairedSurrogates(key []byte) bool {
	for i := 1; i < len(key)-1; i++ {
		if key[i] != '\\' {
			continue
		}
		i++
		if key[i] != 'u' {
			continue
		}
		unit, _ := strconv.ParseUint(string(key[i+1:i+5]), 16, 16)
		i += 4
		switch {
		case unit >= 0xD800 && unit <= 0xDBFF:
			if i+7 >= len(key) || key[i+1] != '\\' || key[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(key[i+3:i+7]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		case unit >= 0xDC00 && unit <= 0xDFFF:
			return false
		}
	}
	return true
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

// persistLocked atomically replaces the backing file. The bool reports whether
// replacement succeeded, even if the subsequent directory sync fails.
// The write lock protects the borrowed entry map during synchronous encoding
// and must remain held through replacement and state reconciliation.
func (kv *KVStore) persistLocked() (bool, error) {
	raw, err := json.Marshal(diskState{Entries: kv.data})
	if err != nil {
		return false, fmt.Errorf("encode state: %w", err)
	}

	path := filepath.Join(kv.opts.DataDir, stateFileName)
	tmp, err := os.CreateTemp(kv.opts.DataDir, stateFileName+".*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temp state: %w", err)
	}
	if err := replaceSnapshot(tmp, path, raw); err != nil {
		return false, err
	}
	syncDirectory := kv.syncStateDir
	if syncDirectory == nil {
		syncDirectory = syncDir
	}
	if err := syncDirectory(kv.opts.DataDir); err != nil {
		return true, fmt.Errorf("sync state dir: %w", err)
	}
	return true, nil
}

type snapshotTempFile interface {
	Name() string
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

func replaceSnapshot(tmp snapshotTempFile, path string, raw []byte) (err error) {
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("remove temp state: %w", removeErr))
			}
		}
	}()

	if _, err := tmp.Write(raw); err != nil {
		return closeSnapshotAfterError(tmp, fmt.Errorf("write temp state: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return closeSnapshotAfterError(tmp, fmt.Errorf("sync temp state: %w", err))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}

func closeSnapshotAfterError(tmp snapshotTempFile, err error) error {
	if closeErr := tmp.Close(); closeErr != nil {
		return errors.Join(err, fmt.Errorf("close temp state: %w", closeErr))
	}
	return err
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
