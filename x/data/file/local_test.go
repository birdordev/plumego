package file

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storefile "github.com/spcent/plumego/store/file"
)

func TestNewLocalStorage(t *testing.T) {
	tmpDir := t.TempDir()

	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatalf("NewLocalStorage failed: %v", err)
	}

	if storage == nil {
		t.Fatal("Storage is nil")
	}

	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		t.Error("Base directory was not created")
	}
}

func TestGenerateIDFromReaderError(t *testing.T) {
	_, err := generateIDFromReader(bytes.NewReader([]byte("short")))
	if err == nil {
		t.Fatal("expected error for short random reader")
	}
}

func TestLocalStorage_Put_Get(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	content := []byte("Hello, World!")

	result, err := storage.Put(ctx, PutOptions{
		TenantID:    "tenant-123",
		Reader:      bytes.NewReader(content),
		FileName:    "test.txt",
		ContentType: "text/plain",
		UploadedBy:  "user-456",
	})
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	if result.ID == "" {
		t.Error("File ID is empty")
	}
	if result.TenantID != "tenant-123" {
		t.Errorf("TenantID = %q, want %q", result.TenantID, "tenant-123")
	}
	if result.Name != "test.txt" {
		t.Errorf("Name = %q, want %q", result.Name, "test.txt")
	}
	if result.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", result.Size, len(content))
	}
	if result.StorageType != "local" {
		t.Errorf("StorageType = %q, want %q", result.StorageType, "local")
	}

	reader, err := storage.Get(ctx, result.Path)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer reader.Close()

	gotContent, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if !bytes.Equal(gotContent, content) {
		t.Errorf("Content = %q, want %q", gotContent, content)
	}
}

func TestLocalStorage_Put_WithExtension(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	tests := []struct {
		fileName    string
		contentType string
		wantExt     string
	}{
		{"test.jpg", "image/jpeg", ".jpg"},
		{"test.png", "image/png", ".png"},
		{"test", "image/jpeg", ".jpg"},
		{"test.txt", "text/plain", ".txt"},
	}

	for _, tt := range tests {
		t.Run(tt.fileName, func(t *testing.T) {
			result, err := storage.Put(ctx, PutOptions{
				TenantID:    "tenant-123",
				Reader:      strings.NewReader("test content"),
				FileName:    tt.fileName,
				ContentType: tt.contentType,
			})
			if err != nil {
				t.Fatalf("Put failed: %v", err)
			}

			if result.Extension != tt.wantExt {
				t.Errorf("Extension = %q, want %q", result.Extension, tt.wantExt)
			}

			if !strings.HasSuffix(result.Path, tt.wantExt) {
				t.Errorf("Path %q does not end with %q", result.Path, tt.wantExt)
			}
		})
	}
}

func TestLocalStorage_Put_GeneratesThumbnailForSupportedImage(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	img := createTestImage(100, 100, color.RGBA{R: 255, A: 255})
	buf := new(bytes.Buffer)
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}

	result, err := storage.Put(t.Context(), PutOptions{
		TenantID:      "tenant-123",
		Reader:        bytes.NewReader(buf.Bytes()),
		FileName:      "avatar.jpg",
		ContentType:   "image/jpeg",
		GenerateThumb: true,
		ThumbWidth:    40,
		ThumbHeight:   40,
	})
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if result.ThumbnailPath == "" {
		t.Fatal("ThumbnailPath is empty for supported image")
	}
	if exists, err := storage.Exists(t.Context(), result.ThumbnailPath); err != nil {
		t.Fatalf("Exists thumbnail failed: %v", err)
	} else if !exists {
		t.Fatal("thumbnail file does not exist")
	}
}

func TestLocalStorage_Put_CleansFilesAfterMetadataError(t *testing.T) {
	saveErr := errors.New("metadata down")
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", &failingSaveMetadata{err: saveErr})
	if err != nil {
		t.Fatal(err)
	}

	img := createTestImage(100, 100, color.RGBA{B: 255, A: 255})
	buf := new(bytes.Buffer)
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}

	_, err = storage.Put(t.Context(), PutOptions{
		TenantID:      "tenant-123",
		Reader:        bytes.NewReader(buf.Bytes()),
		FileName:      "avatar.jpg",
		ContentType:   "image/jpeg",
		GenerateThumb: true,
		ThumbWidth:    40,
		ThumbHeight:   40,
	})
	if !errors.Is(err, saveErr) {
		t.Fatalf("Put error = %v, want metadata error", err)
	}

	err = filepath.WalkDir(tmpDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			t.Fatalf("metadata failure left regular file %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

func TestLocalStorage_Put_SkipsUnsupportedImageThumbnail(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	result, err := storage.Put(t.Context(), PutOptions{
		TenantID:      "tenant-123",
		Reader:        strings.NewReader("not decoded by stdlib image"),
		FileName:      "avatar.webp",
		ContentType:   "image/webp",
		GenerateThumb: true,
		ThumbWidth:    40,
		ThumbHeight:   40,
	})
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if result.Extension != ".webp" {
		t.Fatalf("Extension = %q, want .webp", result.Extension)
	}
	if result.ThumbnailPath != "" {
		t.Fatalf("ThumbnailPath = %q, want empty for unsupported thumbnail format", result.ThumbnailPath)
	}
}

func TestLocalStorage_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	result, err := storage.Put(ctx, PutOptions{
		TenantID: "tenant-123",
		Reader:   strings.NewReader("test content"),
		FileName: "test.txt",
	})
	if err != nil {
		t.Fatal(err)
	}

	exists, err := storage.Exists(ctx, result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("File should exist after Put")
	}

	if err := storage.Delete(ctx, result.Path); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	exists, err = storage.Exists(ctx, result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("File should not exist after Delete")
	}
}

func TestLocalStorage_Delete_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	err = storage.Delete(t.Context(), "tenant-123/2026/02/05/nonexistent.txt")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}

	var fileErr *storefile.Error
	if errors.As(err, &fileErr) {
		if fileErr.Err != storefile.ErrNotFound {
			t.Errorf("Error = %v, want ErrNotFound", fileErr.Err)
		}
	} else {
		t.Errorf("Error type = %T, want *storefile.Error", err)
	}
}

func TestLocalStorage_Stat(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	content := []byte("test content")

	result, err := storage.Put(ctx, PutOptions{
		TenantID: "tenant-123",
		Reader:   bytes.NewReader(content),
		FileName: "test.txt",
	})
	if err != nil {
		t.Fatal(err)
	}

	stat, err := storage.Stat(ctx, result.Path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	if stat.Path != result.Path {
		t.Errorf("Path = %q, want %q", stat.Path, result.Path)
	}
	if stat.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", stat.Size, len(content))
	}
	if stat.IsDir {
		t.Error("IsDir should be false for file")
	}
	if stat.ModifiedTime.IsZero() {
		t.Error("ModifiedTime should not be zero")
	}
}

func TestLocalStorage_List(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	for i := 0; i < 5; i++ {
		_, err := storage.Put(ctx, PutOptions{
			TenantID: "tenant-123",
			Reader:   strings.NewReader("test content"),
			FileName: "test.txt",
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	files, err := storage.List(ctx, "tenant-123", 10)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(files) != 5 {
		t.Errorf("File count = %d, want 5", len(files))
	}

	for _, file := range files {
		if !strings.HasPrefix(file.Path, "tenant-123") {
			t.Errorf("Path %q does not start with tenant-123", file.Path)
		}
	}

	missing, err := storage.List(ctx, "tenant-missing", 10)
	if err != nil {
		t.Fatalf("List missing prefix failed: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing prefix file count = %d, want 0", len(missing))
	}

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = storage.List(canceledCtx, "tenant-123", 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("List canceled error = %v, want context.Canceled", err)
	}
}

func TestLocalStorage_GetURL(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	result, err := storage.Put(ctx, PutOptions{
		TenantID: "tenant-123",
		Reader:   strings.NewReader("test content"),
		FileName: "test.txt",
	})
	if err != nil {
		t.Fatal(err)
	}

	url, err := storage.GetURL(ctx, result.Path, time.Hour)
	if err != nil {
		t.Fatalf("GetURL failed: %v", err)
	}

	expected := "http://example.com/" + result.Path
	if url != expected {
		t.Errorf("URL = %q, want %q", url, expected)
	}
}

func TestLocalStorage_GetURLEscapesPathSegments(t *testing.T) {
	storage, err := NewLocalStorage(t.TempDir(), "http://example.com/static/", nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := storage.GetURL(t.Context(), "tenant-123/2026/02/05/file name #1.txt", time.Hour)
	if err != nil {
		t.Fatalf("GetURL failed: %v", err)
	}

	want := "http://example.com/static/tenant-123/2026/02/05/file%20name%20%231.txt"
	if got != want {
		t.Fatalf("GetURL() = %q, want %q", got, want)
	}
}

func TestLocalStorage_GetURLRejectsUnsafePath(t *testing.T) {
	storage, err := NewLocalStorage(t.TempDir(), "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = storage.GetURL(t.Context(), "../secret.txt", time.Hour)
	if !errors.Is(err, storefile.ErrInvalidPath) {
		t.Fatalf("GetURL() error = %v, want ErrInvalidPath", err)
	}
}

func TestLocalStorage_Copy(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	content := []byte("test content")

	result, err := storage.Put(ctx, PutOptions{
		TenantID: "tenant-123",
		Reader:   bytes.NewReader(content),
		FileName: "source.txt",
	})
	if err != nil {
		t.Fatal(err)
	}

	dstPath := "tenant-123/2026/02/05/copy.txt"
	if err := storage.Copy(ctx, result.Path, dstPath); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	exists, err := storage.Exists(ctx, dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("Copied file should exist")
	}

	reader, err := storage.Get(ctx, dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	copiedContent, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copiedContent, content) {
		t.Errorf("Copied content = %q, want %q", copiedContent, content)
	}
}

func TestLocalStorage_CopyRejectsUnsafePath(t *testing.T) {
	storage, err := NewLocalStorage(t.TempDir(), "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := storage.Copy(t.Context(), "../source.txt", "tenant/copy.txt"); !errors.Is(err, storefile.ErrInvalidPath) {
		t.Fatalf("Copy() source error = %v, want ErrInvalidPath", err)
	}
	if err := storage.Copy(t.Context(), "tenant/source.txt", "../copy.txt"); !errors.Is(err, storefile.ErrInvalidPath) {
		t.Fatalf("Copy() destination error = %v, want ErrInvalidPath", err)
	}
}

func TestLocalStorage_PathSafety(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewLocalStorage(tmpDir, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	attacks := []string{
		"../../../etc/passwd",
		"/etc/passwd",
		"tenant/../../../etc/passwd",
	}

	for _, attack := range attacks {
		t.Run(attack, func(t *testing.T) {
			if _, err := storage.Get(ctx, attack); err == nil {
				t.Error("Expected error for path traversal attack")
			}
			if err := storage.Delete(ctx, attack); err == nil {
				t.Error("Expected error for path traversal attack")
			}
			if _, err := storage.Exists(ctx, attack); err == nil {
				t.Error("Expected error for path traversal attack")
			}
		})
	}
}

func TestLocalStorage_Get_NotFound(t *testing.T) {
	storage, err := NewLocalStorage(t.TempDir(), "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = storage.Get(t.Context(), "tenant/2026/01/01/nonexistent.txt")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
	var fileErr *storefile.Error
	if !errors.As(err, &fileErr) {
		t.Fatalf("expected *storefile.Error, got %T", err)
	}
	if !errors.Is(fileErr.Err, storefile.ErrNotFound) {
		t.Errorf("inner error = %v, want ErrNotFound", fileErr.Err)
	}
}

func TestLocalStorage_Stat_NotFound(t *testing.T) {
	storage, err := NewLocalStorage(t.TempDir(), "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = storage.Stat(t.Context(), "tenant/2026/01/01/nonexistent.txt")
	if !errors.Is(err, storefile.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestLocalStorage_Stat_InvalidPath(t *testing.T) {
	storage, err := NewLocalStorage(t.TempDir(), "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = storage.Stat(t.Context(), "../etc/passwd")
	if !errors.Is(err, storefile.ErrInvalidPath) {
		t.Errorf("expected ErrInvalidPath, got %v", err)
	}
}

func TestLocalStorage_Put_Deduplication(t *testing.T) {
	storage, err := NewLocalStorage(t.TempDir(), "http://example.com", &mockMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	content := []byte("deduplicated content")

	first, err := storage.Put(ctx, PutOptions{
		TenantID: "t1",
		Reader:   bytes.NewReader(content),
		FileName: "dup.txt",
	})
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}

	second, err := storage.Put(ctx, PutOptions{
		TenantID: "t1",
		Reader:   bytes.NewReader(content),
		FileName: "dup.txt",
	})
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if second.Hash != first.Hash {
		t.Errorf("expected same hash for duplicate content, first=%q second=%q", first.Hash, second.Hash)
	}
}

func TestLocalStorage_Put_RejectsUnsafeTenantID(t *testing.T) {
	storage, err := NewLocalStorage(t.TempDir(), "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []string{
		"",
		"../tenant",
		"tenant/child",
		`tenant\child`,
		".",
	}

	for _, tenantID := range tests {
		t.Run(tenantID, func(t *testing.T) {
			_, err := storage.Put(t.Context(), PutOptions{
				TenantID: tenantID,
				Reader:   strings.NewReader("content"),
				FileName: "file.txt",
			})
			if !errors.Is(err, storefile.ErrInvalidPath) {
				t.Fatalf("Put error = %v, want ErrInvalidPath", err)
			}
		})
	}
}

func TestLocalStorage_Put_DeduplicationIsTenantScoped(t *testing.T) {
	metadata := &mockMetadata{}
	storage, err := NewLocalStorage(t.TempDir(), "http://example.com", metadata)
	if err != nil {
		t.Fatal(err)
	}

	content := []byte("same bytes")
	first, err := storage.Put(t.Context(), PutOptions{
		TenantID: "t1",
		Reader:   bytes.NewReader(content),
		FileName: "same.txt",
	})
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}

	second, err := storage.Put(t.Context(), PutOptions{
		TenantID: "t2",
		Reader:   bytes.NewReader(content),
		FileName: "same.txt",
	})
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}

	if second.TenantID != "t2" {
		t.Fatalf("second TenantID = %q, want t2", second.TenantID)
	}
	if second.ID == first.ID {
		t.Fatalf("second upload reused first tenant metadata id %q", second.ID)
	}
	if second.Path == first.Path {
		t.Fatalf("second upload reused first tenant path %q", second.Path)
	}
}

// --- mock MetadataManager ---

type mockMetadata struct {
	store map[string]*File
}

func (m *mockMetadata) Save(_ context.Context, f *File) error {
	if m.store == nil {
		m.store = make(map[string]*File)
	}
	m.store[f.TenantID+":"+f.Hash] = f
	return nil
}

func (m *mockMetadata) Get(_ context.Context, tenantID, id string) (*File, error) {
	if m.store == nil {
		return nil, storefile.ErrNotFound
	}
	for _, f := range m.store {
		if f.TenantID == tenantID && f.ID == id {
			return f, nil
		}
	}
	return nil, storefile.ErrNotFound
}

func (m *mockMetadata) GetByPath(_ context.Context, tenantID, path string) (*File, error) {
	if m.store == nil {
		return nil, storefile.ErrNotFound
	}
	for _, f := range m.store {
		if f.TenantID == tenantID && f.Path == path {
			return f, nil
		}
	}
	return nil, storefile.ErrNotFound
}

func (m *mockMetadata) GetByHash(_ context.Context, tenantID, hash string) (*File, error) {
	if m.store == nil {
		return nil, storefile.ErrNotFound
	}
	if f, ok := m.store[tenantID+":"+hash]; ok {
		return f, nil
	}
	return nil, storefile.ErrNotFound
}

func (m *mockMetadata) List(_ context.Context, _ Query) ([]*File, int64, error) {
	return nil, 0, nil
}

func (m *mockMetadata) Delete(_ context.Context, _, _ string) error { return nil }

func (m *mockMetadata) UpdateAccessTime(_ context.Context, _, _ string) error { return nil }

// Compile-time check
var _ MetadataManager = (*mockMetadata)(nil)

// Benchmarks

func BenchmarkLocalStorage_Put(b *testing.B) {
	tmpDir := b.TempDir()
	storage, _ := NewLocalStorage(tmpDir, "http://example.com", nil)
	ctx := b.Context()
	content := bytes.Repeat([]byte("a"), 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = storage.Put(ctx, PutOptions{
			TenantID: "tenant-123",
			Reader:   bytes.NewReader(content),
			FileName: "test.txt",
		})
	}
}

func BenchmarkLocalStorage_Get(b *testing.B) {
	tmpDir := b.TempDir()
	storage, _ := NewLocalStorage(tmpDir, "http://example.com", nil)
	ctx := b.Context()

	result, _ := storage.Put(ctx, PutOptions{
		TenantID: "tenant-123",
		Reader:   strings.NewReader("test content"),
		FileName: "test.txt",
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reader, _ := storage.Get(ctx, result.Path)
		_, _ = io.Copy(io.Discard, reader)
		reader.Close()
	}
}
