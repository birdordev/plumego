package file

import (
	"io"
	"maps"
	"reflect"
	"slices"
	"time"
)

// File represents file metadata returned by store/file operations. Metadata is
// caller-owned unless a concrete implementation documents that it makes a
// defensive copy. Implementations that retain File values after returning must
// copy mutable maps. Tenant-aware file metadata belongs in extension packages.
type File struct {
	ID           string         `json:"id" db:"id"`
	Name         string         `json:"name" db:"name"`
	Path         string         `json:"path" db:"path"`
	Size         int64          `json:"size" db:"size"`
	MimeType     string         `json:"mime_type" db:"mime_type"`
	Extension    string         `json:"extension" db:"extension"`
	Hash         string         `json:"hash" db:"hash"`
	StorageType  string         `json:"storage_type" db:"storage_type"`
	Metadata     map[string]any `json:"metadata,omitempty" db:"metadata"`
	CreatedAt    time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at" db:"updated_at"`
	LastAccessAt *time.Time     `json:"last_access_at,omitempty" db:"last_access_at"`
	DeletedAt    *time.Time     `json:"deleted_at,omitempty" db:"deleted_at"`
}

// Clone copies f, including its timestamp pointers and metadata. Metadata
// values follow the copy rules of [PutOptions.CloneMetadata].
func (f File) Clone() File {
	clone := f
	clone.Metadata = cloneMetadata(f.Metadata)
	clone.LastAccessAt = cloneTime(f.LastAccessAt)
	clone.DeletedAt = cloneTime(f.DeletedAt)
	return clone
}

// PutOptions contains options for uploading a file. Metadata is caller-owned
// unless a concrete implementation documents that it makes a defensive copy.
// Implementations that retain Metadata after Put returns must copy it. Tenant
// identity is not part of the stable store layer.
type PutOptions struct {
	Reader      io.Reader      // File content
	FileName    string         // Original filename
	ContentType string         // MIME type
	Size        int64          // File size in bytes; -1 may mean unknown
	Metadata    map[string]any // Additional metadata
}

// CloneMetadata copies the upload metadata map. It recursively copies
// map[string]any and []any, and copies the contents of map[string]string,
// []string, and []byte. Other values are retained as-is and may share
// mutable state with the original.
func (o PutOptions) CloneMetadata() map[string]any {
	return cloneMetadata(o.Metadata)
}

// FileStat contains basic file information.
type FileStat struct {
	Path         string    // File path
	Size         int64     // File size in bytes
	ModifiedTime time.Time // Last modified time
	IsDir        bool      // Whether this is a directory
	ContentType  string    // MIME type
}

// Query contains parameters for querying file metadata. Zero values mean no
// stable-layer filter or ordering preference; concrete implementations own
// backend-specific validation. Tenant filtering is not part of the stable store
// layer.
type Query struct {
	MimeType  string
	StartTime time.Time
	EndTime   time.Time
	Page      int
	PageSize  int
	OrderBy   string
}

type metadataReference struct {
	kind    reflect.Kind
	pointer uintptr
	length  int
}

type metadataCopy struct {
	source any
	target any
}

type metadataCloner struct {
	seen    map[metadataReference]any
	pending []metadataCopy
}

func cloneMetadata(metadata map[string]any) map[string]any {
	c := metadataCloner{seen: make(map[metadataReference]any)}
	root := c.cloneValue(metadata).(map[string]any)
	// Register containers before scheduling their children. The worklist keeps
	// call-stack use constant for deep graphs and closes cycles through seen.
	for len(c.pending) > 0 {
		last := len(c.pending) - 1
		work := c.pending[last]
		c.pending[last] = metadataCopy{}
		c.pending = c.pending[:last]
		switch source := work.source.(type) {
		case map[string]any:
			target := work.target.(map[string]any)
			for key, value := range source {
				target[key] = c.cloneValue(value)
			}
		case []any:
			target := work.target.([]any)
			for i, value := range source {
				target[i] = c.cloneValue(value)
			}
		}
	}
	return root
}

func (c *metadataCloner) cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if typed == nil {
			return map[string]any(nil)
		}
		ref := metadataReference{kind: reflect.Map, pointer: reflect.ValueOf(typed).Pointer()}
		if existing, ok := c.seen[ref]; ok {
			return existing
		}
		clone := make(map[string]any, len(typed))
		c.seen[ref] = clone
		c.pending = append(c.pending, metadataCopy{source: typed, target: clone})
		return clone
	case map[string]string:
		return maps.Clone(typed)
	case []any:
		if typed == nil {
			return []any(nil)
		}
		ref := metadataReference{kind: reflect.Slice, pointer: reflect.ValueOf(typed).Pointer(), length: len(typed)}
		if existing, ok := c.seen[ref]; ok {
			return existing
		}
		clone := make([]any, len(typed))
		c.seen[ref] = clone
		c.pending = append(c.pending, metadataCopy{source: typed, target: clone})
		return clone
	case []string:
		return slices.Clone(typed)
	case []byte:
		return slices.Clone(typed)
	default:
		return value
	}
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
