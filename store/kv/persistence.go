package kvstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	if err := (&snapshotMembers{raw: raw}).checkObject(snapshotRoot); err != nil {
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
	}
	kv.data = state.Entries
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

type snapshotReadBudgetError struct {
	limit int64
}

func (e snapshotReadBudgetError) Error() string {
	return fmt.Sprintf("snapshot exceeds the %d-byte input budget", e.limit)
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
		return nil, snapshotReadBudgetError{limit: limit}
	}

	return readSnapshotBytes(file, limit)
}

func readSnapshotBytes(file io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, snapshotReadBudgetError{limit: limit}
	}
	return raw, nil
}

type snapshotObject uint8

const (
	snapshotRoot snapshotObject = iota
	snapshotEntries
	snapshotEntry
)

// The typed decode validates syntax first. This scan checks member names and
// skips values without decoding payloads a second time.
type snapshotMembers struct {
	raw []byte
	pos int
}

func (s *snapshotMembers) checkObject(object snapshotObject) error {
	s.space()
	if s.raw[s.pos] != '{' {
		return errors.New("expected snapshot object")
	}
	s.pos++
	seen := make(map[string]struct{})
	for {
		s.space()
		if s.raw[s.pos] == '}' {
			s.pos++
			return nil
		}
		start := s.pos
		s.skipString()
		key := s.raw[start:s.pos]
		if object == snapshotEntries && !snapshotKeyHasPairedSurrogates(key) {
			return errors.New("snapshot key contains an unpaired Unicode surrogate")
		}
		// A name containing escapes needs the same unquoting as encoding/json.
		name := string(key[1 : len(key)-1])
		if bytes.IndexByte(key, '\\') >= 0 {
			if err := json.Unmarshal(key, &name); err != nil {
				return err
			}
		}
		name = snapshotMemberName(object, name)
		if _, exists := seen[name]; exists {
			return errors.New("snapshot contains a duplicate member")
		}
		seen[name] = struct{}{}
		s.space()
		s.pos++ // ':' between member name and value.
		switch {
		case object == snapshotRoot && name == "entries":
			if err := s.checkObject(snapshotEntries); err != nil {
				return err
			}
		case object == snapshotEntries:
			if err := s.checkObject(snapshotEntry); err != nil {
				return err
			}
		default:
			s.skipValue()
		}
		s.space()
		if s.raw[s.pos] == ',' {
			s.pos++
		}
	}
}

func (s *snapshotMembers) space() {
	for s.pos < len(s.raw) {
		switch s.raw[s.pos] {
		case ' ', '\t', '\r', '\n':
			s.pos++
		default:
			return
		}
	}
}

func (s *snapshotMembers) skipString() {
	s.pos++ // Opening quote.
	for {
		c := s.raw[s.pos]
		s.pos++
		if c == '"' {
			return
		}
		if c == '\\' {
			s.pos++
		}
	}
}

func (s *snapshotMembers) skipValue() {
	s.space()
	depth := 0
	for {
		switch s.raw[s.pos] {
		case '"':
			s.skipString()
			continue
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return
			}
			depth--
		case ',':
			if depth == 0 {
				return
			}
		}
		s.pos++
	}
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
func encodeSnapshot(data map[string]*entry) ([]byte, error) {
	raw, err := json.Marshal(diskState{Entries: data})
	if err != nil {
		return nil, fmt.Errorf("encode state: %w", err)
	}
	return raw, nil
}

func (kv *KVStore) persistSnapshot(raw []byte, onReplace func()) (bool, error) {
	path := filepath.Join(kv.opts.DataDir, stateFileName)
	tmp, err := os.CreateTemp(kv.opts.DataDir, stateFileName+".*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temp state: %w", err)
	}
	if err := replaceSnapshot(tmp, path, raw); err != nil {
		return false, err
	}
	onReplace()
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
	return syncAndCloseDirectory(f)
}

func syncAndCloseDirectory(f interface {
	Sync() error
	Close() error
}) error {
	syncErr := f.Sync()
	if isUnsupportedDirSync(syncErr) {
		syncErr = nil
	}
	if closeErr := f.Close(); closeErr != nil {
		return errors.Join(syncErr, fmt.Errorf("close state dir: %w", closeErr))
	}
	return syncErr
}

func isUnsupportedDirSync(err error) bool {
	return errors.Is(err, os.ErrInvalid) || errors.Is(err, syscall.EINVAL)
}
