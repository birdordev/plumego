// Package fileapi provides an HTTP handler for tenant-aware file operations.
// It composes x/data/file storage and metadata implementations with the
// contract error model.
//
// Tenant identity must be attached via x/tenant/core.WithTenantID before
// reaching any handler method — typically by middleware in the calling
// application. User identity is optional and may be attached via WithUserID.
package fileapi

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	storefile "github.com/birdordev/plumego/store/file"
	datafile "github.com/birdordev/plumego/x/data/file"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
)

const (
	defaultMaxUploadSize   = 100 << 20
	multipartMemoryLimit   = 8 << 20
	multipartOverheadLimit = 1 << 20
)

// Handler provides HTTP endpoints for file operations.
type Handler struct {
	storage       datafile.Storage
	metadata      datafile.MetadataManager
	maxSize       int64
	accessUpdates *accessUpdates
}

type deleteResponse struct {
	Message string `json:"message"`
}

type listResponse struct {
	Items     []*datafile.File `json:"items"`
	Total     int64            `json:"total"`
	Page      int              `json:"page"`
	PageSize  int              `json:"page_size"`
	TotalPage int64            `json:"total_page"`
}

type urlResponse struct {
	URL       string `json:"url"`
	ExpiresIn string `json:"expires_in"`
}

// NewHandler creates a new file handler with a default maximum upload size of
// 100 MiB.
func NewHandler(storage datafile.Storage, metadata datafile.MetadataManager) *Handler {
	return &Handler{
		storage:  storage,
		metadata: metadata,
		maxSize:  defaultMaxUploadSize,
	}
}

// WithMaxSize sets the maximum allowed file size for uploads. A negative
// size restores the default, and zero permits only empty files.
func (h *Handler) WithMaxSize(size int64) *Handler {
	if size < 0 {
		size = defaultMaxUploadSize
	}
	h.maxSize = size
	return h
}

func routeParam(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	return contract.RequestContextFromContext(r.Context()).Params[name]
}

func (h *Handler) loadAuthorizedFile(w http.ResponseWriter, r *http.Request) (*datafile.File, string, bool) {
	ctx := r.Context()
	fileID := routeParam(r, "id")
	if fileID == "" {
		writeMissingFileID(w, r)
		return nil, "", false
	}

	tenantID := tenantcore.TenantIDFromContext(ctx)
	if tenantID == "" {
		writeMissingTenantID(w, r)
		return nil, "", false
	}

	fileMeta, err := h.metadata.Get(ctx, tenantID, fileID)
	if err != nil {
		writeFileMetadataError(w, r, err)
		return nil, "", false
	}

	if fileMeta.TenantID != tenantID {
		writeFileAccessDenied(w, r)
		return nil, "", false
	}

	return fileMeta, fileID, true
}

// Upload handles file upload via multipart form.
// POST /files
// Multipart form fields: file (required), generate_thumb, thumb_width, thumb_height.
// Requires tenant identity in request context.
func (h *Handler) Upload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantID := tenantcore.TenantIDFromContext(ctx)
	if tenantID == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeBadRequest).
			Message("missing tenant id in context").
			Build())
		return
	}

	userID := UserIDFromContext(ctx)

	// Bound the whole request separately from the selected file and the parser's
	// in-memory threshold. Saturate the addition for very large configured sizes.
	bodyLimit := min(h.maxSize, math.MaxInt64-multipartOverheadLimit) + multipartOverheadLimit
	if r.ContentLength > bodyLimit {
		writeUploadTooLarge(w, r)
		return
	}
	formWasParsed := r.MultipartForm != nil
	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	if err := r.ParseMultipartForm(min(h.maxSize, multipartMemoryLimit)); err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) || errors.Is(err, multipart.ErrMessageTooLarge) {
			writeUploadTooLarge(w, r)
			return
		}
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeBadRequest).
			Message("invalid multipart form").
			Build())
		return
	}

	if !formWasParsed {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}

	f, header, err := r.FormFile("file")
	if err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Code(contract.CodeRequired).
			Message("file field is required").
			Build())
		return
	}
	defer func() { _ = f.Close() }()

	if header.Size > h.maxSize {
		writeUploadTooLarge(w, r)
		return
	}

	generateThumb := r.PostFormValue("generate_thumb") == "true"
	thumbWidth, _ := strconv.Atoi(r.PostFormValue("thumb_width"))
	thumbHeight, _ := strconv.Atoi(r.PostFormValue("thumb_height"))

	opts := datafile.PutOptions{
		TenantID:      tenantID,
		Reader:        f,
		FileName:      header.Filename,
		ContentType:   header.Header.Get("Content-Type"),
		GenerateThumb: generateThumb,
		ThumbWidth:    thumbWidth,
		ThumbHeight:   thumbHeight,
		UploadedBy:    userID,
		Metadata:      make(map[string]any),
	}

	var result *datafile.File
	if store, ok := h.storage.(*datafile.S3Storage); ok && !formWasParsed && header.Size > multipartMemoryLimit {
		opts.Size = header.Size
		result, err = store.PutStaged(ctx, opts, f)
	} else {
		result, err = h.storage.Put(ctx, opts)
	}
	if err != nil {
		writeFileInternalError(w, r, "upload failed")
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, result, nil)
}

func writeUploadTooLarge(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypePayloadTooLarge).
		Message("upload exceeds size limit").
		Build())
}

// Download streams file content. Updating the access time is best effort.
// GET /files/{id}
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", privateDownloadCacheControl)
	ctx := r.Context()
	fileMeta, fileID, ok := h.loadAuthorizedFile(w, r)
	if !ok {
		return
	}

	if updates := h.accessUpdates; updates != nil {
		updates.enqueue(accessKey{tenant: fileMeta.TenantID, file: fileID})
	} else {
		_ = h.metadata.UpdateAccessTime(ctx, fileMeta.TenantID, fileID)
	}

	ranged, supportsRanges := h.storage.(downloadRangeStorage)
	if supportsRanges && needsDownloadMetadata(r) {
		h.downloadWithRanges(w, r, fileMeta, ranged)
		return
	}

	reader, err := h.storage.Get(ctx, fileMeta.Path)
	if err != nil {
		writeFileInternalError(w, r, "failed to read file")
		return
	}
	defer func() { _ = reader.Close() }()

	content, seekable := reader.(io.ReadSeeker)
	setDownloadHeaders(w.Header(), fileMeta, seekable || fileMeta.Size >= 0)
	if seekable {
		if r.Method != http.MethodGet {
			r = withoutDownloadRange(r)
		}
		http.ServeContent(privateDownloadWriter{ResponseWriter: w}, r, fileMeta.Name, fileMeta.UpdatedAt, content)
		return
	}
	if fileMeta.Size >= 0 {
		http.ServeContent(privateDownloadWriter{ResponseWriter: w, streamOnly: !supportsRanges},
			withoutDownloadRange(r), fileMeta.Name, fileMeta.UpdatedAt, forwardDownload{reader, fileMeta.Size})
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, reader)
}

func setDownloadHeaders(header http.Header, fileMeta *datafile.File, validators bool) {
	header.Set("Content-Type", fileMeta.MimeType)
	header.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileMeta.Name))
	if hash := fileMeta.Hash; validators && hash != "" {
		// 将文件哈希编码为符合语法的实体标签。
		tag := sha256.Sum256([]byte(hash))
		header.Set("ETag", fmt.Sprintf(`"%x"`, tag))
	}
}

const privateDownloadCacheControl = "private, no-store"

func withoutDownloadRange(r *http.Request) *http.Request {
	if r.Header.Get("Range") == "" {
		return r
	}
	withoutRange := r.WithContext(r.Context())
	withoutRange.Header = r.Header.Clone()
	withoutRange.Header.Del("Range")
	return withoutRange
}

// forwardDownload 只处理 ServeContent 在顺序读取前执行的长度查询。
type forwardDownload struct {
	io.Reader
	size int64
}

func (c forwardDownload) Seek(offset int64, whence int) (int64, error) {
	switch {
	case offset == 0 && whence == io.SeekEnd:
		return c.size, nil
	case offset == 0 && whence == io.SeekStart:
		return 0, nil
	default:
		return 0, errors.ErrUnsupported
	}
}

type privateDownloadWriter struct {
	http.ResponseWriter
	streamOnly bool
}

func (w privateDownloadWriter) WriteHeader(status int) {
	// ServeContent 在若干错误路径上清理缓存头，私有下载仍须禁止缓存。
	w.Header().Set("Cache-Control", privateDownloadCacheControl)
	if w.streamOnly {
		w.Header().Del("Accept-Ranges")
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w privateDownloadWriter) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(w.ResponseWriter, r)
}

// GetInfo returns file metadata.
// GET /files/{id}/info
func (h *Handler) GetInfo(w http.ResponseWriter, r *http.Request) {
	fileMeta, _, ok := h.loadAuthorizedFile(w, r)
	if !ok {
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, fileMeta, nil)
}

// Delete soft-deletes a file.
// DELETE /files/{id}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	_, fileID, ok := h.loadAuthorizedFile(w, r)
	if !ok {
		return
	}

	tenantID := tenantcore.TenantIDFromContext(ctx)
	if err := h.metadata.Delete(ctx, tenantID, fileID); err != nil {
		writeFileMetadataError(w, r, err)
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, deleteResponse{Message: "file deleted"}, nil)
}

// List returns a paginated list of files.
// GET /files?page=1&page_size=20&mime_type=image/jpeg&uploaded_by=user1
// Requires tenant identity in request context.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	params := r.URL.Query()
	query := datafile.Query{Page: 1, PageSize: 20}

	if p := params.Get("page"); p != "" {
		v, err := parsePositiveIntParam("page", p, 0)
		if err != nil {
			writeInvalidQueryError(w, r, "page")
			return
		}
		query.Page = v
	}

	if ps := params.Get("page_size"); ps != "" {
		v, err := parsePositiveIntParam("page_size", ps, 100)
		if err != nil {
			writeInvalidQueryError(w, r, "page_size")
			return
		}
		query.PageSize = v
	}

	tenantID := tenantcore.TenantIDFromContext(ctx)
	if tenantID == "" {
		writeMissingTenantID(w, r)
		return
	}
	query.TenantID = tenantID
	query.UploadedBy = params.Get("uploaded_by")
	query.MimeType = params.Get("mime_type")
	query.OrderBy = params.Get("order_by")

	if s := params.Get("start_time"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeInvalidQueryError(w, r, "start_time")
			return
		}
		query.StartTime = t
	}

	if s := params.Get("end_time"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeInvalidQueryError(w, r, "end_time")
			return
		}
		query.EndTime = t
	}

	files, total, err := h.metadata.List(ctx, query)
	if err != nil {
		writeFileInternalError(w, r, "list failed")
		return
	}

	pageSize := int64(query.PageSize)
	totalPages := total / pageSize
	if total%pageSize > 0 {
		totalPages++
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, listResponse{
		Items:     files,
		Total:     total,
		Page:      query.Page,
		PageSize:  query.PageSize,
		TotalPage: totalPages,
	}, nil)
}

// GetURL returns a temporary access URL for the file. An explicit expiry must
// be a positive number of seconds representable as a time.Duration.
// GET /files/{id}/url?expiry=3600
func (h *Handler) GetURL(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	expiry := 15 * time.Minute
	if s := r.URL.Query().Get("expiry"); s != "" {
		seconds, err := strconv.ParseInt(s, 10, 64)
		if err != nil || seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
			writeInvalidQueryError(w, r, "expiry")
			return
		}
		expiry = time.Duration(seconds) * time.Second
	}

	fileMeta, _, ok := h.loadAuthorizedFile(w, r)
	if !ok {
		return
	}

	fileURL, err := h.storage.GetURL(ctx, fileMeta.Path, expiry)
	if err != nil {
		writeFileInternalError(w, r, "failed to generate url")
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, urlResponse{
		URL:       fileURL,
		ExpiresIn: strconv.FormatInt(int64(expiry/time.Second), 10),
	}, nil)
}

func writeMissingFileID(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeValidation).
		Code(contract.CodeBadRequest).
		Message("missing file id").
		Build())
}

func writeMissingTenantID(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeValidation).
		Code(contract.CodeBadRequest).
		Message("missing tenant id in context").
		Build())
}

func writeFileAccessDenied(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeForbidden).
		Message("access denied").
		Build())
}

func writeFileMetadataError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, storefile.ErrNotFound) {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeNotFound).
			Message("file not found").
			Build())
	} else {
		writeFileInternalError(w, r, "metadata error")
	}
}

func parsePositiveIntParam(field, value string, max int) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid %s", field)
	}
	if max > 0 && n > max {
		return 0, fmt.Errorf("%s out of range", field)
	}
	return n, nil
}

func writeInvalidQueryError(w http.ResponseWriter, r *http.Request, field string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeValidation).
		Code(contract.CodeInvalidQuery).
		Message("invalid query parameter").
		Detail("field", field).
		Build())
}

func writeFileInternalError(w http.ResponseWriter, r *http.Request, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(contract.TypeInternal).
		Code(contract.CodeInternalError).
		Message(message).
		Build())
}
