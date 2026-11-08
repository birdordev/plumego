package file

import (
	"io"
	"reflect"
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

// Clone returns a copy of f with mutable fields detached from the original.
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

// CloneMetadata returns a copy of the upload metadata map.
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

type metadataCloner map[metadataReference]any

func cloneMetadata(metadata map[string]any) map[string]any {
	return make(metadataCloner).cloneMap(metadata)
}

func (c metadataCloner) cloneMap(metadata map[string]any) map[string]any {
	if metadata == nil {
		return nil
	}
	ref := metadataReference{kind: reflect.Map, pointer: reflect.ValueOf(metadata).Pointer()}
	if existing, ok := c[ref]; ok {
		return existing.(map[string]any)
	}
	clone := make(map[string]any, len(metadata))
	c[ref] = clone
	for key, value := range metadata {
		clone[key] = c.cloneValue(value)
	}
	return clone
}

func (c metadataCloner) cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return c.cloneMap(typed)
	case map[string]string:
		if typed == nil {
			return map[string]string(nil)
		}
		clone := make(map[string]string, len(typed))
		for key, value := range typed {
			clone[key] = value
		}
		return clone
	case []any:
		if typed == nil {
			return []any(nil)
		}
		ref := metadataReference{kind: reflect.Slice, pointer: reflect.ValueOf(typed).Pointer(), length: len(typed)}
		if existing, ok := c[ref]; ok {
			return existing.([]any)
		}
		clone := make([]any, len(typed))
		c[ref] = clone
		for i, value := range typed {
			clone[i] = c.cloneValue(value)
		}
		return clone
	case []string:
		return append([]string(nil), typed...)
	case []byte:
		return append([]byte(nil), typed...)
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
