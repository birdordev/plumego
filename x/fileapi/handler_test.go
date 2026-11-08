package fileapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
	storefile "github.com/birdordev/plumego/store/file"
	datafile "github.com/birdordev/plumego/x/data/file"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
)

func withRouteParam(r *http.Request, name, value string) *http.Request {
	rc := contract.RequestContextFromContext(r.Context())
	params := make(map[string]string, len(rc.Params)+1)
	for k, v := range rc.Params {
		params[k] = v
	}
	params[name] = value
	rc.Params = params
	return r.WithContext(contract.WithRequestContext(r.Context(), rc))
}

// --- mock Storage ---

type mockStorage struct {
	putFunc     func(ctx context.Context, opts datafile.PutOptions) (*datafile.File, error)
	getFunc     func(ctx context.Context, path string) (io.ReadCloser, error)
	deleteFunc  func(ctx context.Context, path string) error
	getURLFunc  func(ctx context.Context, path string, expiry time.Duration) (string, error)
	deletePaths []string
}

func (m *mockStorage) Put(ctx context.Context, opts datafile.PutOptions) (*datafile.File, error) {
	if m.putFunc != nil {
		return m.putFunc(ctx, opts)
	}
	return &datafile.File{File: storefile.File{ID: "test-id", Path: "test/path.txt"}, TenantID: opts.TenantID}, nil
}

func (m *mockStorage) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, path)
	}
	return io.NopCloser(strings.NewReader("test content")), nil
}

func (m *mockStorage) Delete(ctx context.Context, path string) error {
	m.deletePaths = append(m.deletePaths, path)
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, path)
	}
	return nil
}

func (m *mockStorage) Exists(ctx context.Context, path string) (bool, error) { return true, nil }

func (m *mockStorage) Stat(ctx context.Context, path string) (*storefile.FileStat, error) {
	return &storefile.FileStat{Path: path, Size: 1024}, nil
}

func (m *mockStorage) List(ctx context.Context, prefix string, limit int) ([]*storefile.FileStat, error) {
	return []*storefile.FileStat{}, nil
}

func (m *mockStorage) GetURL(ctx context.Context, path string, expiry time.Duration) (string, error) {
	if m.getURLFunc != nil {
		return m.getURLFunc(ctx, path, expiry)
	}
	return "http://example.com/" + path, nil
}

func (m *mockStorage) Copy(ctx context.Context, src, dst string) error { return nil }

// --- mock MetadataManager ---

type mockMetadataManager struct {
	getFunc              func(ctx context.Context, tenantID, id string) (*datafile.File, error)
	listFunc             func(ctx context.Context, q datafile.Query) ([]*datafile.File, int64, error)
	deleteFunc           func(ctx context.Context, tenantID, id string) error
	updateAccessTimeFunc func(ctx context.Context, tenantID, id string) error
}

func (m *mockMetadataManager) Save(ctx context.Context, file *datafile.File) error { return nil }

func (m *mockMetadataManager) Get(ctx context.Context, tenantID, id string) (*datafile.File, error) {
	if m.getFunc != nil {
		return m.getFunc(ctx, tenantID, id)
	}
	return &datafile.File{
		File:     storefile.File{ID: id, Path: "test/path.txt", Name: "test.txt", Size: 1024, MimeType: "text/plain"},
		TenantID: tenantID,
	}, nil
}

func (m *mockMetadataManager) GetByPath(ctx context.Context, tenantID, path string) (*datafile.File, error) {
	return &datafile.File{File: storefile.File{Path: path}, TenantID: tenantID}, nil
}

func (m *mockMetadataManager) GetByHash(ctx context.Context, tenantID, hash string) (*datafile.File, error) {
	return nil, nil
}

func (m *mockMetadataManager) List(ctx context.Context, q datafile.Query) ([]*datafile.File, int64, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, q)
	}
	return []*datafile.File{}, 0, nil
}

func (m *mockMetadataManager) Delete(ctx context.Context, tenantID, id string) error {
	if m.deleteFunc != nil {
		return m.deleteFunc(ctx, tenantID, id)
	}
	return nil
}

func (m *mockMetadataManager) UpdateAccessTime(ctx context.Context, tenantID, id string) error {
	if m.updateAccessTimeFunc != nil {
		return m.updateAccessTimeFunc(ctx, tenantID, id)
	}
	return nil
}

// Compile-time checks
var _ datafile.Storage = (*mockStorage)(nil)
var _ datafile.MetadataManager = (*mockMetadataManager)(nil)

type successEnvelope struct {
	Data json.RawMessage `json:"data"`
}

type errorEnvelope struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
	} `json:"error"`
}

func decodeResponseData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var envelope successEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response envelope: %v", err)
	}
	if len(envelope.Data) == 0 {
		t.Fatalf("response envelope has empty data: %s", rec.Body.String())
	}
	var out T
	if err := json.Unmarshal(envelope.Data, &out); err != nil {
		t.Fatalf("decode response data: %v", err)
	}
	return out
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var envelope errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return envelope
}

// --- Tests ---

func TestNewHandler(t *testing.T) {
	h := NewHandler(&mockStorage{}, &mockMetadataManager{})
	if h == nil {
		t.Fatal("Handler is nil")
	}
	if h.maxSize != 100<<20 {
		t.Errorf("Default maxSize = %d, want %d", h.maxSize, 100<<20)
	}
}

func TestHandler_WithMaxSize(t *testing.T) {
	h := NewHandler(&mockStorage{}, &mockMetadataManager{}).WithMaxSize(50 << 20)
	if h.maxSize != 50<<20 {
		t.Errorf("maxSize = %d, want %d", h.maxSize, 50<<20)
	}
}

func TestHandler_Upload(t *testing.T) {
	storage := &mockStorage{
		putFunc: func(ctx context.Context, opts datafile.PutOptions) (*datafile.File, error) {
			return &datafile.File{
				File:     storefile.File{ID: "test-id", Name: opts.FileName, Path: "test/path.txt", Size: 100},
				TenantID: opts.TenantID,
			}, nil
		},
	}
	h := NewHandler(storage, &mockMetadataManager{})

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/files", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	ctx = WithUserID(ctx, "user-456")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	h.Upload(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	result := decodeResponseData[datafile.File](t, w)
	if result.ID != "test-id" {
		t.Errorf("ID = %q, want %q", result.ID, "test-id")
	}
	if result.TenantID != "tenant-123" {
		t.Errorf("TenantID = %q, want %q", result.TenantID, "tenant-123")
	}
}

func TestHandler_Upload_MissingTenantID(t *testing.T) {
	h := NewHandler(&mockStorage{}, &mockMetadataManager{})

	req := httptest.NewRequest(http.MethodPost, "/files", nil)
	w := httptest.NewRecorder()
	h.Upload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandler_Upload_StorageErrorDoesNotLeak(t *testing.T) {
	h := NewHandler(&mockStorage{
		putFunc: func(ctx context.Context, opts datafile.PutOptions) (*datafile.File, error) {
			return nil, errors.New("backend password=secret")
		},
	}, &mockMetadataManager{})

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/files", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(tenantcore.WithTenantID(req.Context(), "tenant-123"))
	w := httptest.NewRecorder()
	h.Upload(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	errResp := decodeError(t, w)
	if errResp.Error.Message != "upload failed" {
		t.Fatalf("Message = %q, want upload failed", errResp.Error.Message)
	}
	if strings.Contains(w.Body.String(), "password=secret") {
		t.Fatalf("response leaked backend error: %s", w.Body.String())
	}
}

func TestHandler_Download(t *testing.T) {
	storage := &mockStorage{
		getFunc: func(ctx context.Context, path string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("file content")), nil
		},
	}
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{
				File:     storefile.File{ID: id, Path: "test/path.txt", Name: "test.txt", Size: 12, MimeType: "text/plain"},
				TenantID: "tenant-123",
			}, nil
		},
	}
	h := NewHandler(storage, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files/test-id", nil)
	req = withRouteParam(req, "id", "test-id")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Download(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("Content-Type = %q", w.Header().Get("Content-Type"))
	}
	if w.Body.String() != "file content" {
		t.Errorf("Body = %q, want %q", w.Body.String(), "file content")
	}
}

func TestHandler_Download_CrossTenant(t *testing.T) {
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{
				File:     storefile.File{ID: id, Path: "test/path.txt", Name: "test.txt", Size: 12, MimeType: "text/plain"},
				TenantID: "tenant-other",
			}, nil
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files/test-id", nil)
	req = withRouteParam(req, "id", "test-id")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-attacker")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Download(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d (cross-tenant access should be denied)", w.Code, http.StatusForbidden)
	}
}

func TestHandler_Download_NotFound(t *testing.T) {
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return nil, storefile.ErrNotFound
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files/nonexistent", nil)
	req = withRouteParam(req, "id", "nonexistent")
	req = req.WithContext(tenantcore.WithTenantID(req.Context(), "tenant-123"))
	w := httptest.NewRecorder()
	h.Download(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandler_GetInfo(t *testing.T) {
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{
				File:     storefile.File{ID: id, Name: "test.txt", Size: 1024, MimeType: "text/plain"},
				TenantID: "tenant-123",
			}, nil
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files/test-id/info", nil)
	req = withRouteParam(req, "id", "test-id")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.GetInfo(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	result := decodeResponseData[datafile.File](t, w)
	if result.ID != "test-id" {
		t.Errorf("ID = %q, want %q", result.ID, "test-id")
	}
}

func TestHandler_GetInfo_CrossTenant(t *testing.T) {
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{
				File:     storefile.File{ID: id, Name: "test.txt", Size: 1024, MimeType: "text/plain"},
				TenantID: "tenant-other",
			}, nil
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files/test-id/info", nil)
	req = withRouteParam(req, "id", "test-id")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-attacker")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.GetInfo(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d (cross-tenant access should be denied)", w.Code, http.StatusForbidden)
	}
}

func TestHandler_Delete(t *testing.T) {
	var deletedTenantID, deletedFileID string
	storage := &mockStorage{}
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{File: storefile.File{ID: id, Path: "test/path.txt"}, TenantID: "tenant-123"}, nil
		},
		deleteFunc: func(ctx context.Context, tenantID, id string) error {
			deletedTenantID = tenantID
			deletedFileID = id
			return nil
		},
	}
	h := NewHandler(storage, metadata)

	req := httptest.NewRequest(http.MethodDelete, "/files/test-id", nil)
	req = withRouteParam(req, "id", "test-id")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	result := decodeResponseData[map[string]string](t, w)
	if result["message"] != "file deleted" {
		t.Errorf("Message = %q, want %q", result["message"], "file deleted")
	}
	if deletedTenantID != "tenant-123" || deletedFileID != "test-id" {
		t.Fatalf("metadata delete = (%q, %q), want (tenant-123, test-id)", deletedTenantID, deletedFileID)
	}
	if len(storage.deletePaths) != 0 {
		t.Fatalf("storage delete paths = %v, want none; fileapi Delete owns metadata soft delete only", storage.deletePaths)
	}
}

func TestHandler_Delete_NotFound(t *testing.T) {
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return nil, storefile.ErrNotFound
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodDelete, "/files/nonexistent", nil)
	req = withRouteParam(req, "id", "nonexistent")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandler_Delete_CrossTenant(t *testing.T) {
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{File: storefile.File{ID: id, Path: "test/path.txt"}, TenantID: "tenant-other"}, nil
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodDelete, "/files/test-id", nil)
	req = withRouteParam(req, "id", "test-id")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-attacker")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d (cross-tenant delete should be denied)", w.Code, http.StatusForbidden)
	}
}

func TestHandler_List(t *testing.T) {
	metadata := &mockMetadataManager{
		listFunc: func(ctx context.Context, q datafile.Query) ([]*datafile.File, int64, error) {
			return []*datafile.File{
				{File: storefile.File{ID: "file1", Name: "test1.txt"}},
				{File: storefile.File{ID: "file2", Name: "test2.txt"}},
			}, 2, nil
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files?page=1&page_size=20", nil)
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.List(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	result := decodeResponseData[listResponse](t, w)
	if result.Total != 2 {
		t.Errorf("Total = %v, want 2", result.Total)
	}
	if len(result.Items) != 2 {
		t.Errorf("Items = %d, want 2", len(result.Items))
	}
	if result.Page != 1 || result.PageSize != 20 || result.TotalPage != 1 {
		t.Errorf("unexpected pagination: %+v", result)
	}
}

func TestHandler_List_MissingTenant(t *testing.T) {
	h := NewHandler(&mockStorage{}, &mockMetadataManager{})

	req := httptest.NewRequest(http.MethodGet, "/files?page=1&page_size=20", nil)
	w := httptest.NewRecorder()
	h.List(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d (missing tenant should return 400)", w.Code, http.StatusBadRequest)
	}
}

func TestHandler_List_WithFilters(t *testing.T) {
	var capturedQuery datafile.Query
	metadata := &mockMetadataManager{
		listFunc: func(ctx context.Context, q datafile.Query) ([]*datafile.File, int64, error) {
			capturedQuery = q
			return []*datafile.File{}, 0, nil
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files?mime_type=image/jpeg&page=2&page_size=10", nil)
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.List(w, req)

	if capturedQuery.TenantID != "tenant-123" {
		t.Errorf("TenantID = %q, want tenant-123", capturedQuery.TenantID)
	}
	if capturedQuery.MimeType != "image/jpeg" {
		t.Errorf("MimeType = %q, want image/jpeg", capturedQuery.MimeType)
	}
	if capturedQuery.Page != 2 {
		t.Errorf("Page = %d, want 2", capturedQuery.Page)
	}
	if capturedQuery.PageSize != 10 {
		t.Errorf("PageSize = %d, want 10", capturedQuery.PageSize)
	}
}

func TestHandler_List_InvalidQueryParams(t *testing.T) {
	tests := []struct {
		name  string
		query string
		field string
	}{
		{name: "page", query: "page=bad", field: "page"},
		{name: "page size", query: "page_size=101", field: "page_size"},
		{name: "start time", query: "start_time=bad", field: "start_time"},
		{name: "end time", query: "end_time=bad", field: "end_time"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(&mockStorage{}, &mockMetadataManager{})
			req := httptest.NewRequest(http.MethodGet, "/files?"+tt.query, nil)
			req = req.WithContext(tenantcore.WithTenantID(req.Context(), "tenant-123"))
			w := httptest.NewRecorder()
			h.List(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("Status = %d, want %d", w.Code, http.StatusBadRequest)
			}
			errResp := decodeError(t, w)
			if errResp.Error.Code != contract.CodeInvalidQuery {
				t.Fatalf("Code = %q, want %q", errResp.Error.Code, contract.CodeInvalidQuery)
			}
			if got := errResp.Error.Details["field"]; got != tt.field {
				t.Fatalf("field detail = %v, want %q", got, tt.field)
			}
		})
	}
}

func TestHandler_List_MetadataErrorDoesNotLeak(t *testing.T) {
	h := NewHandler(&mockStorage{}, &mockMetadataManager{
		listFunc: func(ctx context.Context, q datafile.Query) ([]*datafile.File, int64, error) {
			return nil, 0, errors.New("database password=secret")
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/files", nil)
	req = req.WithContext(tenantcore.WithTenantID(req.Context(), "tenant-123"))
	w := httptest.NewRecorder()
	h.List(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	errResp := decodeError(t, w)
	if errResp.Error.Message != "list failed" {
		t.Fatalf("Message = %q, want list failed", errResp.Error.Message)
	}
	if strings.Contains(w.Body.String(), "password=secret") {
		t.Fatalf("response leaked metadata error: %s", w.Body.String())
	}
}

func TestHandler_GetURL(t *testing.T) {
	storage := &mockStorage{
		getURLFunc: func(ctx context.Context, path string, expiry time.Duration) (string, error) {
			return "https://example.com/presigned-url", nil
		},
	}
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{File: storefile.File{ID: id, Path: "test/path.txt"}, TenantID: "tenant-123"}, nil
		},
	}
	h := NewHandler(storage, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files/test-id/url?expiry=3600", nil)
	req = withRouteParam(req, "id", "test-id")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.GetURL(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	result := decodeResponseData[map[string]string](t, w)
	if result["url"] != "https://example.com/presigned-url" {
		t.Errorf("URL = %q", result["url"])
	}
	if result["expires_in"] != "3600" {
		t.Errorf("ExpiresIn = %q, want 3600", result["expires_in"])
	}
}

func TestHandler_GetURL_StorageErrorDoesNotLeak(t *testing.T) {
	storage := &mockStorage{
		getURLFunc: func(ctx context.Context, path string, expiry time.Duration) (string, error) {
			return "", errors.New("signing key leaked")
		},
	}
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{File: storefile.File{ID: id, Path: "test/path.txt"}, TenantID: "tenant-123"}, nil
		},
	}
	h := NewHandler(storage, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files/test-id/url", nil)
	req = withRouteParam(req, "id", "test-id")
	req = req.WithContext(tenantcore.WithTenantID(req.Context(), "tenant-123"))
	w := httptest.NewRecorder()
	h.GetURL(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	errResp := decodeError(t, w)
	if errResp.Error.Message != "failed to generate url" {
		t.Fatalf("Message = %q, want failed to generate url", errResp.Error.Message)
	}
	if strings.Contains(w.Body.String(), "signing key") {
		t.Fatalf("response leaked storage error: %s", w.Body.String())
	}
}

func TestHandler_GetURL_InvalidExpiry(t *testing.T) {
	h := NewHandler(&mockStorage{}, &mockMetadataManager{})

	req := httptest.NewRequest(http.MethodGet, "/files/test-id/url?expiry=bad", nil)
	req = withRouteParam(req, "id", "test-id")
	req = req.WithContext(tenantcore.WithTenantID(req.Context(), "tenant-123"))
	w := httptest.NewRecorder()
	h.GetURL(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	errResp := decodeError(t, w)
	if errResp.Error.Code != contract.CodeInvalidQuery {
		t.Fatalf("Code = %q, want %q", errResp.Error.Code, contract.CodeInvalidQuery)
	}
	if got := errResp.Error.Details["field"]; got != "expiry" {
		t.Fatalf("field detail = %v, want expiry", got)
	}
}

func TestHandler_GetURL_CrossTenant(t *testing.T) {
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{File: storefile.File{ID: id, Path: "test/path.txt"}, TenantID: "tenant-other"}, nil
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files/test-id/url", nil)
	req = withRouteParam(req, "id", "test-id")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-attacker")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.GetURL(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d (cross-tenant URL access should be denied)", w.Code, http.StatusForbidden)
	}
}

func TestHandler_Download_MetadataError(t *testing.T) {
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return nil, errors.New("db error")
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files/test-id", nil)
	req = withRouteParam(req, "id", "test-id")
	req = req.WithContext(tenantcore.WithTenantID(req.Context(), "tenant-123"))
	w := httptest.NewRecorder()
	h.Download(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	errResp := decodeError(t, w)
	if errResp.Error.Message != "metadata error" {
		t.Fatalf("Message = %q, want metadata error", errResp.Error.Message)
	}
	if strings.Contains(w.Body.String(), "db error") {
		t.Fatalf("response leaked metadata error: %s", w.Body.String())
	}
}

func TestHandler_Download_MissingFileID(t *testing.T) {
	h := NewHandler(&mockStorage{}, &mockMetadataManager{})

	req := httptest.NewRequest(http.MethodGet, "/files/", nil)
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Download(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandler_Download_MissingTenantID(t *testing.T) {
	h := NewHandler(&mockStorage{}, &mockMetadataManager{})

	req := httptest.NewRequest(http.MethodGet, "/files/test-id", nil)
	req = withRouteParam(req, "id", "test-id")
	w := httptest.NewRecorder()
	h.Download(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestRouteParam_NilRequest(t *testing.T) {
	got := routeParam(nil, "id")
	if got != "" {
		t.Errorf("routeParam(nil) = %q, want \"\"", got)
	}
}

func TestUserIDFromContext_Empty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	got := UserIDFromContext(req.Context())
	if got != "" {
		t.Errorf("UserIDFromContext = %q, want \"\"", got)
	}
}

func TestUserIDFromContext_NilContext(t *testing.T) {
	got := UserIDFromContext(nil)
	if got != "" {
		t.Errorf("UserIDFromContext(nil) = %q, want \"\"", got)
	}
}

func TestHandler_Upload_MissingFileField(t *testing.T) {
	h := NewHandler(&mockStorage{}, &mockMetadataManager{})

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	// No file field added — just close the writer
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/files", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Upload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandler_Download_StorageError(t *testing.T) {
	storage := &mockStorage{
		getFunc: func(ctx context.Context, path string) (io.ReadCloser, error) {
			return nil, errors.New("disk error")
		},
	}
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{
				File:     storefile.File{ID: id, Path: "test/path.txt", Name: "test.txt", Size: 12, MimeType: "text/plain"},
				TenantID: "tenant-123",
			}, nil
		},
	}
	h := NewHandler(storage, metadata)

	req := httptest.NewRequest(http.MethodGet, "/files/test-id", nil)
	req = withRouteParam(req, "id", "test-id")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Download(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandler_Delete_MetadataError(t *testing.T) {
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{File: storefile.File{ID: id, Path: "test/path.txt"}, TenantID: "tenant-123"}, nil
		},
		deleteFunc: func(ctx context.Context, tenantID, id string) error {
			return errors.New("db error")
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	req := httptest.NewRequest(http.MethodDelete, "/files/test-id", nil)
	req = withRouteParam(req, "id", "test-id")
	ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// Benchmarks

func BenchmarkHandler_Upload(b *testing.B) {
	h := NewHandler(&mockStorage{}, &mockMetadataManager{})

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()
	bodyBytes := body.Bytes()
	contentType := writer.FormDataContentType()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/files", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", contentType)
		ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		h.Upload(w, req)
	}
}

func BenchmarkHandler_Download(b *testing.B) {
	metadata := &mockMetadataManager{
		getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
			return &datafile.File{
				File:     storefile.File{ID: id, Path: "test/path.txt", Name: "test.txt", Size: 12, MimeType: "text/plain"},
				TenantID: "tenant-123",
			}, nil
		},
	}
	h := NewHandler(&mockStorage{}, metadata)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/files/test-id", nil)
		req = withRouteParam(req, "id", "test-id")
		ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
		req = req.WithContext(ctx)
		w := httptest.NewRecorder()
		h.Download(w, req)
	}
}
