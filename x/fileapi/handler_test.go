package fileapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
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
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", got)
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

func TestHandler_ListTotalPages(t *testing.T) {
	for _, tc := range []struct {
		total int64
		pages int64
	}{
		{0, 0},
		{4, 2},
		{5, 3},
		{math.MaxInt64, math.MaxInt64/2 + 1},
	} {
		metadata := &mockMetadataManager{
			listFunc: func(context.Context, datafile.Query) ([]*datafile.File, int64, error) {
				return nil, tc.total, nil
			},
		}
		h := NewHandler(&mockStorage{}, metadata)
		req := httptest.NewRequest(http.MethodGet, "/files?page_size=2", nil)
		rec := httptest.NewRecorder()
		h.List(rec, tenantcore.RequestWithTenantID(req, "tenant"))
		if rec.Code != http.StatusOK {
			t.Fatalf("List status = %d", rec.Code)
		}
		result := decodeResponseData[listResponse](t, rec)
		if result.Total != tc.total || result.TotalPage != tc.pages {
			t.Errorf("total=%d pages=%d, want %d and %d", result.Total, result.TotalPage, tc.total, tc.pages)
		}
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
	for _, tc := range []struct{ query, seconds string }{{"", "900"}, {"?expiry=3600", "3600"}} {
		t.Run(tc.seconds, func(t *testing.T) {
			storage := &mockStorage{
				getURLFunc: func(ctx context.Context, path string, expiry time.Duration) (string, error) {
					return "https://example.invalid/report?format=json&lang=en", nil
				},
			}
			metadata := &mockMetadataManager{
				getFunc: func(ctx context.Context, tenantID, id string) (*datafile.File, error) {
					return &datafile.File{File: storefile.File{ID: id, Path: "test/path.txt"}, TenantID: "tenant-123"}, nil
				},
			}
			h := NewHandler(storage, metadata)

			req := httptest.NewRequest(http.MethodGet, "/files/test-id/url"+tc.query, nil)
			req = withRouteParam(req, "id", "test-id")
			ctx := tenantcore.WithTenantID(req.Context(), "tenant-123")
			req = req.WithContext(ctx)
			w := httptest.NewRecorder()
			h.GetURL(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
			}

			result := decodeResponseData[map[string]string](t, w)
			if result["url"] != "https://example.invalid/report?format=json&lang=en" {
				t.Errorf("URL = %q", result["url"])
			}
			if result["expires_in"] != tc.seconds {
				t.Errorf("ExpiresIn = %q, want 3600", result["expires_in"])
			}
		})
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

func TestHandlerUploadSizeLimits(t *testing.T) {
	const limit = 32
	for _, tc := range []struct {
		name                    string
		size, fieldSize, status int
	}{
		{"below", limit - 1, 0, http.StatusOK},
		{"exact", limit, 0, http.StatusOK},
		{"over file limit", limit + 1, 0, http.StatusRequestEntityTooLarge},
		{"over request limit", limit, multipartOverheadLimit, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			storage := &mockStorage{putFunc: func(_ context.Context, opts datafile.PutOptions) (*datafile.File, error) {
				calls++
				size, err := io.Copy(io.Discard, opts.Reader)
				if err != nil || size != int64(tc.size) {
					t.Fatalf("stored file size = %d, err = %v; want %d", size, err, tc.size)
				}
				return &datafile.File{}, nil
			}}
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("file", "test.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(part, strings.Repeat("x", tc.size)); err != nil {
				t.Fatal(err)
			}
			if tc.fieldSize > 0 {
				if err := writer.WriteField("extra", strings.Repeat("x", tc.fieldSize)); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/files", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			// Exercise the parser's streaming cap without Content-Length.
			req.ContentLength = -1
			req = req.WithContext(tenantcore.WithTenantID(req.Context(), "tenant"))
			rec := httptest.NewRecorder()
			NewHandler(storage, &mockMetadataManager{}).WithMaxSize(limit).Upload(rec, req)
			wantCalls := 0
			if tc.status == http.StatusOK {
				wantCalls = 1
			}
			if rec.Code != tc.status || calls != wantCalls {
				t.Fatalf("status = %d, store calls = %d; want %d and %d", rec.Code, calls, tc.status, wantCalls)
			}
		})
	}
}

func TestHandlerUploadThumbnailOptionsComeFromMultipart(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fields        map[string]string
		generate      bool
		width, height int
	}{
		{"multipart wins", map[string]string{"generate_thumb": "true", "thumb_width": "40", "thumb_height": "60"}, true, 40, 60},
		{"query only", nil, false, 0, 0},
		{"existing field conversion", map[string]string{"generate_thumb": "false", "thumb_width": "invalid", "thumb_height": "60"}, false, 0, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			part, err := form.CreateFormFile("file", "sample.txt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(part, "fixture"); err != nil {
				t.Fatal(err)
			}
			for name, value := range tc.fields {
				if err := form.WriteField(name, value); err != nil {
					t.Fatal(err)
				}
			}
			if err := form.Close(); err != nil {
				t.Fatal(err)
			}

			var got datafile.PutOptions
			storage := &mockStorage{putFunc: func(_ context.Context, opts datafile.PutOptions) (*datafile.File, error) {
				got = opts
				return &datafile.File{File: storefile.File{ID: "sample"}, TenantID: opts.TenantID}, nil
			}}
			h := NewHandler(storage, &mockMetadataManager{})
			queryGenerate := "false"
			if !tc.generate {
				queryGenerate = "true"
			}
			req := httptest.NewRequest(http.MethodPost, "/files?generate_thumb="+queryGenerate+"&thumb_width=100&thumb_height=200", &body)
			req.Header.Set("Content-Type", form.FormDataContentType())
			req = req.WithContext(tenantcore.WithTenantID(req.Context(), "sample"))
			w := httptest.NewRecorder()
			h.Upload(w, req)
			if w.Code != http.StatusOK || got.GenerateThumb != tc.generate || got.ThumbWidth != tc.width || got.ThumbHeight != tc.height {
				t.Errorf("status=%d thumbnail=(%v, %d, %d), want 200 (%v, %d, %d)", w.Code, got.GenerateThumb, got.ThumbWidth, got.ThumbHeight, tc.generate, tc.width, tc.height)
			}
		})
	}
}

func TestHandlerGetURLExpiryDurationBounds(t *testing.T) {
	const maxSeconds = math.MaxInt64 / int64(time.Second)
	for _, tc := range []struct {
		expiry string
		want   int64
	}{
		{"", 900},
		{"3600", 3600},
		{strconv.FormatInt(maxSeconds, 10), maxSeconds},
		{strconv.FormatInt(maxSeconds+1, 10), 0},
		{"9223372036854775808", 0},
		{"-1", 0},
		{"0", 0},
	} {
		t.Run(tc.expiry, func(t *testing.T) {
			var calls int
			var got time.Duration
			storage := &mockStorage{getURLFunc: func(_ context.Context, _ string, expiry time.Duration) (string, error) {
				calls++
				got = expiry
				return "http://example.invalid", nil
			}}
			r := httptest.NewRequest(http.MethodGet, "/files/fixture/url?expiry="+tc.expiry, nil)
			r = r.WithContext(tenantcore.WithTenantID(r.Context(), "fixture"))
			r = withRouteParam(r, "id", "fixture")
			w := httptest.NewRecorder()
			NewHandler(storage, &mockMetadataManager{}).GetURL(w, r)
			if tc.want == 0 {
				if w.Code != http.StatusBadRequest || calls != 0 {
					t.Errorf("status=%d storage calls=%d", w.Code, calls)
				}
				return
			}
			if w.Code != http.StatusOK || calls != 1 || got != time.Duration(tc.want)*time.Second {
				t.Fatalf("status=%d storage calls=%d expiry=%v", w.Code, calls, got)
			}
			if result := decodeResponseData[urlResponse](t, w); result.ExpiresIn != strconv.FormatInt(tc.want, 10) {
				t.Errorf("expires_in=%q, want %d", result.ExpiresIn, tc.want)
			}
		})
	}
}

type downloadTestReader struct {
	io.Reader
	closeCalls int
}

func (r *downloadTestReader) Close() error {
	r.closeCalls++
	return nil
}

type downloadTestReadError struct{ err error }

func (r downloadTestReadError) Read([]byte) (int, error) { return 0, r.err }

type downloadStatusRecorder struct {
	*httptest.ResponseRecorder
	statuses []int
}

func (w *downloadStatusRecorder) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
	w.ResponseRecorder.WriteHeader(status)
}

func TestDownloadClosesReaderAfterCommittedReadFailure(t *testing.T) {
	reader := &downloadTestReader{Reader: io.MultiReader(strings.NewReader("partial"), downloadTestReadError{err: io.ErrUnexpectedEOF})}
	storage := &mockStorage{getFunc: func(context.Context, string) (io.ReadCloser, error) { return reader, nil }}
	metadata := &mockMetadataManager{getFunc: func(_ context.Context, tenantID, id string) (*datafile.File, error) {
		return &datafile.File{
			File:     storefile.File{ID: id, Path: "item.txt", Name: "item.txt", Size: 12, MimeType: "text/plain"},
			TenantID: tenantID,
		}, nil
	}}
	h := NewHandler(storage, metadata)
	req := withRouteParam(httptest.NewRequest(http.MethodGet, "/files/item", nil), "id", "item")
	req = req.WithContext(tenantcore.WithTenantID(req.Context(), "test-tenant"))
	w := &downloadStatusRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.Download(w, req)
	if len(w.statuses) != 1 || w.statuses[0] != http.StatusOK || w.Body.String() != "partial" || reader.closeCalls != 1 {
		t.Errorf("statuses = %v, body = %q, reader closes = %d", w.statuses, w.Body.String(), reader.closeCalls)
	}
}

func TestFileListPropagatesRequestCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		queryStarted := make(chan struct{})
		observed := make(chan error, 1)
		metadata := &mockMetadataManager{listFunc: func(ctx context.Context, _ datafile.Query) ([]*datafile.File, int64, error) {
			close(queryStarted)
			<-ctx.Done()
			observed <- ctx.Err()
			return nil, 0, ctx.Err()
		}}
		handler := NewHandler(&mockStorage{}, metadata)
		request := httptest.NewRequest(http.MethodGet, "/files", nil)
		ctx, cancel := context.WithCancel(tenantcore.WithTenantID(request.Context(), "sample"))
		defer cancel()
		recorder := &downloadStatusRecorder{ResponseRecorder: httptest.NewRecorder()}
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			handler.List(recorder, request.WithContext(ctx))
		}()

		<-queryStarted
		cancel()
		synctest.Wait()
		select {
		case <-finished:
		default:
			t.Fatal("List did not exit after cancellation")
		}
		if err := <-observed; !errors.Is(err, context.Canceled) {
			t.Fatalf("metadata observed %v", err)
		}
		if recorder.Code != http.StatusInternalServerError || len(recorder.statuses) != 1 {
			t.Fatalf("List response = %d, statuses = %v", recorder.Code, recorder.statuses)
		}
	})
}

type failingDownloadWriter struct {
	downloadStatusRecorder
	writes int
}

func (w *failingDownloadWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, io.ErrClosedPipe
}

func TestDownloadClosesReaderAfterCommittedWriteFailure(t *testing.T) {
	reader := &downloadTestReader{Reader: strings.NewReader("file contents")}
	storage := &mockStorage{getFunc: func(context.Context, string) (io.ReadCloser, error) { return reader, nil }}
	metadata := &mockMetadataManager{getFunc: func(_ context.Context, tenantID, id string) (*datafile.File, error) {
		return &datafile.File{
			File:     storefile.File{ID: id, Path: "item.txt", Name: "item.txt", Size: 13, MimeType: "text/plain"},
			TenantID: tenantID,
		}, nil
	}}
	req := withRouteParam(httptest.NewRequest(http.MethodGet, "/files/item", nil), "id", "item")
	req = req.WithContext(tenantcore.WithTenantID(req.Context(), "test-tenant"))
	recorder := &failingDownloadWriter{downloadStatusRecorder: downloadStatusRecorder{ResponseRecorder: httptest.NewRecorder()}}
	NewHandler(storage, metadata).Download(recorder, req)
	if len(recorder.statuses) != 1 || recorder.statuses[0] != http.StatusOK || recorder.writes != 1 || reader.closeCalls != 1 {
		t.Errorf("status commits = %v, writes = %d, reader closes = %d", recorder.statuses, recorder.writes, reader.closeCalls)
	}
}

func TestUploadMultipartFileOwnership(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	t.Setenv("TMP", temp)
	t.Setenv("TEMP", temp)
	for _, alreadyParsed := range []bool{false, true} {
		t.Run(strconv.FormatBool(alreadyParsed), func(t *testing.T) {
			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			part, err := w.CreateFormFile("file", "asset.txt")
			if err != nil {
				t.Fatal(err)
			}
			const length = multipartMemoryLimit + 1
			if _, err := io.WriteString(part, strings.Repeat("x", length)); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/files", &body)
			req.Header.Set("Content-Type", w.FormDataContentType())
			req = req.WithContext(tenantcore.WithTenantID(req.Context(), "tenant"))
			if alreadyParsed {
				if err := req.ParseMultipartForm(multipartMemoryLimit); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := req.MultipartForm.RemoveAll(); err != nil {
						t.Error(err)
					}
				})
			}
			called := false
			storage := &mockStorage{putFunc: func(_ context.Context, opts datafile.PutOptions) (*datafile.File, error) {
				called = true
				if file, ok := opts.Reader.(*os.File); !ok || filepath.Dir(file.Name()) != temp {
					t.Errorf("multipart reader did not spill to private directory: %T", opts.Reader)
				}
				return nil, errors.New("storage failure")
			}}
			rec := httptest.NewRecorder()
			NewHandler(storage, &mockMetadataManager{}).Upload(rec, req)
			if !called || rec.Code != http.StatusInternalServerError {
				t.Fatalf("upload called=%t, status=%d", called, rec.Code)
			}
			if alreadyParsed {
				f, _, err := req.FormFile("file")
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				var first [1]byte
				if _, err := io.ReadFull(f, first[:]); err != nil || first[0] != 'x' {
					t.Fatalf("caller-owned file: %q, %v", first, err)
				}
			} else {
				files, err := os.ReadDir(temp)
				if err != nil || len(files) != 0 {
					t.Fatalf("handler-owned files left behind: %v (%v)", files, err)
				}
			}
		})
	}
}

func TestGetURLPropagatesRequestCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		request := withRouteParam(httptest.NewRequest(http.MethodGet, "/files/asset/url", nil), "id", "asset")
		ctx, cancel := context.WithCancel(tenantcore.WithTenantID(request.Context(), "sample"))
		defer cancel()
		entered := make(chan struct{})
		observed := make(chan error, 1)
		storage := &mockStorage{getURLFunc: func(got context.Context, path string, expiry time.Duration) (string, error) {
			if got != ctx || path != "test/path.txt" || expiry != 15*time.Minute {
				t.Errorf("GetURL storage inputs: ctx matches = %t, path = %q, expiry = %v", got == ctx, path, expiry)
			}
			close(entered)
			<-got.Done()
			observed <- got.Err()
			return "", errors.Join(got.Err(), errors.New("storage diagnostic"))
		}}
		h := NewHandler(storage, &mockMetadataManager{})
		rec := &downloadStatusRecorder{ResponseRecorder: httptest.NewRecorder()}
		done := make(chan struct{})
		go func() {
			defer close(done)
			h.GetURL(rec, request.WithContext(ctx))
		}()
		<-entered
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("GetURL did not return after cancellation")
		}
		if err := <-observed; !errors.Is(err, context.Canceled) {
			t.Fatalf("storage observed %v", err)
		}
		if rec.Code != http.StatusInternalServerError || len(rec.statuses) != 1 ||
			!strings.Contains(rec.Body.String(), "failed to generate url") || strings.Contains(rec.Body.String(), "storage diagnostic") {
			t.Fatalf("GetURL response = %v, %q", rec.statuses, rec.Body.String())
		}
	})
}

func TestDownloadIgnoresAccessTimeUpdateFailure(t *testing.T) {
	req := withRouteParam(httptest.NewRequest(http.MethodGet, "/files/item", nil), "id", "item")
	req = tenantcore.RequestWithTenantID(req, "test-tenant")
	updated := false
	reader := &downloadTestReader{Reader: strings.NewReader("test content")}
	metadata := &mockMetadataManager{updateAccessTimeFunc: func(ctx context.Context, tenant, id string) error {
		if ctx != req.Context() || tenant != "test-tenant" || id != "item" {
			t.Errorf("UpdateAccessTime got wrong context or %q/%q", tenant, id)
		}
		if updated {
			t.Error("UpdateAccessTime was called more than once")
		}
		updated = true
		return errors.New("update diagnostic")
	}}
	storage := &mockStorage{getFunc: func(ctx context.Context, path string) (io.ReadCloser, error) {
		if ctx != req.Context() || path != "test/path.txt" || !updated {
			t.Errorf("unexpected read arguments or order: path=%q updated=%t", path, updated)
		}
		return reader, nil
	}}
	w := &downloadStatusRecorder{ResponseRecorder: httptest.NewRecorder()}
	NewHandler(storage, metadata).Download(w, req)
	if len(w.statuses) != 1 || w.statuses[0] != http.StatusOK || w.Body.String() != "test content" || reader.closeCalls != 1 {
		t.Fatalf("statuses=%v body=%q closes=%d", w.statuses, w.Body.String(), reader.closeCalls)
	}
}

func TestUploadNegativeLimitRestoresDefault(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "test.txt")
	if err != nil {
		t.Fatal(err)
	}
	const contents = "payload"
	if _, err := io.WriteString(part, contents); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	calls := 0
	storage := &mockStorage{putFunc: func(_ context.Context, opts datafile.PutOptions) (*datafile.File, error) {
		calls++
		got, err := io.ReadAll(opts.Reader)
		if err != nil || string(got) != contents {
			t.Errorf("stored contents = %q, error = %v", got, err)
		}
		return &datafile.File{}, nil
	}}
	h := NewHandler(storage, &mockMetadataManager{}).WithMaxSize(0)
	upload := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/files", bytes.NewReader(body.Bytes()))
		r.Header.Set("Content-Type", form.FormDataContentType())
		r = r.WithContext(tenantcore.WithTenantID(r.Context(), "tenant"))
		rec := httptest.NewRecorder()
		h.Upload(rec, r)
		return rec
	}
	if rec := upload(); rec.Code != http.StatusRequestEntityTooLarge || calls != 0 {
		t.Fatalf("with zero limit: status = %d, store calls = %d", rec.Code, calls)
	}
	h.WithMaxSize(-1)
	if h.maxSize != 100<<20 {
		t.Errorf("restored limit = %d; want 100 MiB", h.maxSize)
	}
	if rec := upload(); rec.Code != http.StatusOK || calls != 1 {
		t.Fatalf("with restored limit: status = %d, store calls = %d", rec.Code, calls)
	}
}

func TestFileListForwardsTimeRange(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/files?page=2&page_size=5&start_time=2026-01-02T03:04:05%2B02:00&end_time=2026-01-02T06:00:00Z", nil)
	ctx := tenantcore.WithTenantID(r.Context(), "tenant")
	r = r.WithContext(ctx)
	calls := 0
	metadata := &mockMetadataManager{listFunc: func(got context.Context, q datafile.Query) ([]*datafile.File, int64, error) {
		calls++
		if got != ctx || q.TenantID != "tenant" || !q.StartTime.Equal(time.Date(2026, 1, 2, 1, 4, 5, 0, time.UTC)) ||
			!q.EndTime.Equal(time.Date(2026, 1, 2, 6, 0, 0, 0, time.UTC)) {
			t.Errorf("metadata query = %+v", q)
		}
		return nil, 8, nil
	}}
	rec := httptest.NewRecorder()
	NewHandler(&mockStorage{}, metadata).List(rec, r)
	var env struct {
		Data listResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || calls != 1 || env.Data.Page != 2 || env.Data.PageSize != 5 || env.Data.TotalPage != 2 {
		t.Fatalf("response = %d %+v, calls=%d", rec.Code, env.Data, calls)
	}
}

func TestUploadMalformedMultipartStopsBeforeStorage(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/files", strings.NewReader("--fixture\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.txt\"\r\n\r\ntruncated"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=fixture")
	r = r.WithContext(tenantcore.WithTenantID(r.Context(), "tenant"))
	calls := 0
	storage := &mockStorage{putFunc: func(context.Context, datafile.PutOptions) (*datafile.File, error) {
		calls++
		return &datafile.File{}, nil
	}}
	rec := httptest.NewRecorder()
	NewHandler(storage, &mockMetadataManager{}).Upload(rec, r)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusBadRequest || env.Error.Code != contract.CodeBadRequest || calls != 0 {
		t.Errorf("response=%d %+v storage calls=%d", rec.Code, env, calls)
	}
}
