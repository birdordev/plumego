package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	storefile "github.com/birdordev/plumego/store/file"
)

// LocalStorage implements Storage using the local filesystem.
// Files are organised as: basePath/{tenantID}/{YYYY}/{MM}/{DD}/{id}{ext}
type LocalStorage struct {
	basePath  string
	baseURL   string
	metadata  MetadataManager
	imageProc *imageProcessor
}

// NewLocalStorage creates a new local filesystem storage.
func NewLocalStorage(basePath, baseURL string, metadata MetadataManager) (*LocalStorage, error) {
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, &storefile.Error{
			Op:   "NewLocalStorage",
			Path: basePath,
			Err:  err,
		}
	}

	return &LocalStorage{
		basePath:  basePath,
		baseURL:   baseURL,
		metadata:  metadata,
		imageProc: newImageProcessor(),
	}, nil
}

// Put uploads a file to local storage under the tenant's directory tree.
func (s *LocalStorage) Put(ctx context.Context, opts PutOptions) (*File, error) {
	tenantID, err := cleanTenantID(opts.TenantID)
	if err != nil {
		return nil, &storefile.Error{Op: "Put", Path: opts.TenantID, Err: err}
	}

	fileID, err := generateID()
	if err != nil {
		return nil, &storefile.Error{Op: "Put", Path: opts.TenantID, Err: err}
	}

	ext := filepath.Ext(opts.FileName)
	if ext == "" && opts.ContentType != "" {
		ext = mimeToExt(opts.ContentType)
	}

	// Path: {tenantID}/{YYYY}/{MM}/{DD}/{id}{ext}
	now := time.Now()
	relativePath := filepath.Join(
		tenantID,
		fmt.Sprintf("%d", now.Year()),
		fmt.Sprintf("%02d", now.Month()),
		fmt.Sprintf("%02d", now.Day()),
		fileID+ext,
	)

	fullPath := filepath.Join(s.basePath, relativePath)

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, &storefile.Error{Op: "Put", Path: fullPath, Err: err}
	}

	// Write to temp file, compute hash atomically
	tmpFile, err := os.CreateTemp(dir, "upload-*")
	if err != nil {
		return nil, &storefile.Error{Op: "Put", Path: fullPath, Err: err}
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmpFile, hash), opts.Reader)
	if err != nil {
		_ = tmpFile.Close()
		return nil, &storefile.Error{Op: "Put", Path: fullPath, Err: err}
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return nil, &storefile.Error{Op: "Put", Path: fullPath, Err: err}
	}
	if err := tmpFile.Close(); err != nil {
		return nil, &storefile.Error{Op: "Put", Path: fullPath, Err: err}
	}

	hashString := hex.EncodeToString(hash.Sum(nil))

	// Deduplication: return existing record if same hash exists
	if s.metadata != nil {
		existing, err := s.metadata.GetByHash(ctx, tenantID, hashString)
		if err == nil && existing != nil {
			return existing, nil
		}
	}

	if err := os.Rename(tmpPath, fullPath); err != nil {
		return nil, &storefile.Error{Op: "Put", Path: fullPath, Err: err}
	}
	if err := syncDir(dir); err != nil {
		_ = os.Remove(fullPath)
		return nil, &storefile.Error{Op: "Put", Path: dir, Err: err}
	}

	file := &File{
		File: storefile.File{
			ID:          fileID,
			Name:        opts.FileName,
			Path:        relativePath,
			Size:        size,
			MimeType:    opts.ContentType,
			Extension:   ext,
			Hash:        hashString,
			StorageType: "local",
			Metadata:    opts.Metadata,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		TenantID:   tenantID,
		UploadedBy: opts.UploadedBy,
	}

	if opts.ContentType != "" && s.imageProc.IsImage(opts.ContentType) {
		f, err := os.Open(fullPath)
		if err == nil {
			imgInfo, err := s.imageProc.GetInfo(f)
			_ = f.Close()
			if err == nil {
				file.Width = imgInfo.Width
				file.Height = imgInfo.Height
			}
		}

		if opts.GenerateThumb && s.imageProc.SupportsThumbnail(opts.ContentType) {
			thumbPath, err := s.generateThumbnail(fullPath, relativePath, opts.ThumbWidth, opts.ThumbHeight)
			if err == nil {
				file.ThumbnailPath = thumbPath
			}
		}
	}

	if s.metadata != nil {
		if err := s.metadata.Save(ctx, file); err != nil {
			return nil, cleanupLocalPutAfterMetadataError(err, fullPath, file.ThumbnailPath, s.basePath)
		}
	}

	return file, nil
}

func cleanupLocalPutAfterMetadataError(saveErr error, fullPath, thumbnailPath, basePath string) error {
	var cleanupErrs []error
	if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup stored file %q after metadata failure: %w", fullPath, err))
	}
	if thumbnailPath != "" {
		thumbFullPath := filepath.Join(basePath, thumbnailPath)
		if err := os.Remove(thumbFullPath); err != nil && !os.IsNotExist(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup thumbnail %q after metadata failure: %w", thumbFullPath, err))
		}
	}
	if len(cleanupErrs) == 0 {
		return saveErr
	}
	return errors.Join(append([]error{fmt.Errorf("save metadata: %w", saveErr)}, cleanupErrs...)...)
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// Get retrieves a file from local storage.
func (s *LocalStorage) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	if !isPathSafe(path) {
		return nil, &storefile.Error{Op: "Get", Path: path, Err: storefile.ErrInvalidPath}
	}

	fullPath := filepath.Join(s.basePath, path)

	f, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &storefile.Error{Op: "Get", Path: path, Err: storefile.ErrNotFound}
		}
		return nil, &storefile.Error{Op: "Get", Path: path, Err: err}
	}

	return f, nil
}

// Delete removes a file from local storage.
func (s *LocalStorage) Delete(ctx context.Context, path string) error {
	if !isPathSafe(path) {
		return &storefile.Error{Op: "Delete", Path: path, Err: storefile.ErrInvalidPath}
	}

	fullPath := filepath.Join(s.basePath, path)

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return &storefile.Error{Op: "Delete", Path: path, Err: storefile.ErrNotFound}
		}
		return &storefile.Error{Op: "Delete", Path: path, Err: err}
	}

	return nil
}

// Exists checks if a file exists in local storage.
func (s *LocalStorage) Exists(ctx context.Context, path string) (bool, error) {
	if !isPathSafe(path) {
		return false, storefile.ErrInvalidPath
	}

	_, err := os.Stat(filepath.Join(s.basePath, path))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// Stat returns file information from local storage.
func (s *LocalStorage) Stat(ctx context.Context, path string) (*storefile.FileStat, error) {
	if !isPathSafe(path) {
		return nil, storefile.ErrInvalidPath
	}

	info, err := os.Stat(filepath.Join(s.basePath, path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storefile.ErrNotFound
		}
		return nil, err
	}

	return &storefile.FileStat{
		Path:         path,
		Size:         info.Size(),
		ModifiedTime: info.ModTime(),
		IsDir:        info.IsDir(),
	}, nil
}

// List returns files in local storage matching the prefix.
func (s *LocalStorage) List(ctx context.Context, prefix string, limit int) ([]*storefile.FileStat, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !isPathSafe(prefix) {
		return nil, storefile.ErrInvalidPath
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var results []*storefile.FileStat
	count := 0
	fullPath := filepath.Join(s.basePath, prefix)

	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			return results, nil
		}
		return nil, err
	}

	errStopWalk := errors.New("stop local list")
	err := filepath.WalkDir(fullPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if limit > 0 && count >= limit {
			return errStopWalk
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			relPath, err := filepath.Rel(s.basePath, path)
			if err != nil {
				return err
			}
			results = append(results, &storefile.FileStat{
				Path:         relPath,
				Size:         info.Size(),
				ModifiedTime: info.ModTime(),
			})
			count++
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errStopWalk) {
			return results, nil
		}
		return nil, err
	}

	return results, nil
}

// GetURL returns a static URL for accessing the file.
func (s *LocalStorage) GetURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	if !isPathSafe(path) {
		return "", &storefile.Error{Op: "GetURL", Path: path, Err: storefile.ErrInvalidPath}
	}
	return strings.TrimRight(s.baseURL, "/") + "/" + escapeLocalURLPath(path), nil
}

// Copy copies a file within local storage.
func (s *LocalStorage) Copy(ctx context.Context, srcPath, dstPath string) error {
	if !isPathSafe(srcPath) || !isPathSafe(dstPath) {
		return storefile.ErrInvalidPath
	}

	src, err := os.Open(filepath.Join(s.basePath, srcPath))
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	dstFullPath := filepath.Join(s.basePath, dstPath)
	return writeLocalFileAtomic(dstFullPath, src)
}

func writeLocalFileAtomic(dstFullPath string, src io.Reader) error {
	dstDir := filepath.Dir(dstFullPath)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dstDir, ".write-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := io.Copy(tmpFile, src); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, dstFullPath); err != nil {
		return err
	}
	if err := syncDir(dstDir); err != nil {
		_ = os.Remove(dstFullPath)
		return err
	}

	return nil
}

func (s *LocalStorage) generateThumbnail(srcPath, relativePath string, width, height int) (string, error) {
	if width <= 0 {
		width = 200
	}
	if height <= 0 {
		height = 200
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = src.Close() }()

	thumbReader, err := s.imageProc.Thumbnail(src, width, height)
	if err != nil {
		return "", err
	}

	ext := filepath.Ext(relativePath)
	thumbRelPath := strings.TrimSuffix(relativePath, ext) + "_thumb" + ext
	thumbFullPath := filepath.Join(s.basePath, thumbRelPath)

	if err := os.MkdirAll(filepath.Dir(thumbFullPath), 0755); err != nil {
		return "", err
	}

	if err := writeLocalFileAtomic(thumbFullPath, thumbReader); err != nil {
		return "", err
	}

	return thumbRelPath, nil
}

func escapeLocalURLPath(p string) string {
	parts := strings.Split(p, string(filepath.Separator))
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		escaped = append(escaped, url.PathEscape(part))
	}
	return strings.Join(escaped, "/")
}
