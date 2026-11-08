// Package file provides tenant-aware file storage implementations backed by
// the store/file interfaces. It adds tenant identity to every operation and
// organises stored objects by tenant in both the filesystem path and the
// metadata database.
package file

import (
	"context"
	"io"
	"time"

	storefile "github.com/birdordev/plumego/store/file"
)

// File extends the core store/file.File record with tenant identity and
// media-processing metadata.
type File struct {
	storefile.File
	TenantID      string `json:"tenant_id" db:"tenant_id"`
	Width         int    `json:"width,omitempty" db:"width"`
	Height        int    `json:"height,omitempty" db:"height"`
	ThumbnailPath string `json:"thumbnail_path,omitempty" db:"thumbnail_path"`
	UploadedBy    string `json:"uploaded_by" db:"uploaded_by"`
}

// Clone returns a copy of f with all mutable fields detached from the original.
func (f File) Clone() File {
	clone := f
	clone.File = f.File.Clone()
	return clone
}

// PutOptions contains options for uploading a file, including the tenant
// under which the file should be stored.
type PutOptions struct {
	TenantID      string         // Tenant ID for path isolation and metadata
	Reader        io.Reader      // File content
	FileName      string         // Original filename
	ContentType   string         // MIME type
	Size          int64          // File size (-1 if unknown)
	UploadedBy    string         // User ID of uploader
	GenerateThumb bool           // Whether to generate thumbnail
	ThumbWidth    int            // Thumbnail width (default 200)
	ThumbHeight   int            // Thumbnail height (default 200)
	Metadata      map[string]any // Additional metadata
}

// Query contains parameters for querying tenant-scoped file metadata.
type Query struct {
	TenantID   string
	UploadedBy string
	MimeType   string
	StartTime  time.Time
	EndTime    time.Time
	Page       int
	PageSize   int
	OrderBy    string
}

// Storage is a tenant-aware superset of store/file.Storage.
// It accepts PutOptions (which carries TenantID) and returns tenant-tagged
// File records. The Get/Delete/Exists/Stat/List/GetURL/Copy methods operate
// on paths that already encode the tenant (as set by Put).
type Storage interface {
	Put(ctx context.Context, opts PutOptions) (*File, error)
	Get(ctx context.Context, path string) (io.ReadCloser, error)
	Delete(ctx context.Context, path string) error
	Exists(ctx context.Context, path string) (bool, error)
	Stat(ctx context.Context, path string) (*storefile.FileStat, error)
	List(ctx context.Context, prefix string, limit int) ([]*storefile.FileStat, error)
	GetURL(ctx context.Context, path string, expiry time.Duration) (string, error)
	Copy(ctx context.Context, srcPath, dstPath string) error
}

// MetadataManager manages tenant-scoped file metadata in a persistent store.
type MetadataManager interface {
	Save(ctx context.Context, file *File) error
	Get(ctx context.Context, tenantID, id string) (*File, error)
	GetByPath(ctx context.Context, tenantID, path string) (*File, error)
	GetByHash(ctx context.Context, tenantID, hash string) (*File, error)
	List(ctx context.Context, query Query) ([]*File, int64, error) // requires Query.TenantID
	Delete(ctx context.Context, tenantID, id string) error
	UpdateAccessTime(ctx context.Context, tenantID, id string) error
}
