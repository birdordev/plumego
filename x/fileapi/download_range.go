package fileapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	datafile "github.com/birdordev/plumego/x/data/file"
)

// downloadRangeStorage 是提供端可选择实现的区间读取能力。
type downloadRangeStorage interface {
	GetRange(ctx context.Context, path string, offset, length, size int64) (io.ReadCloser, error)
}

func needsDownloadMetadata(r *http.Request) bool {
	return r.Method == http.MethodHead || r.Method == http.MethodGet &&
		(r.Header.Get("Range") != "" || r.Header.Get("If-Match") != "" ||
			r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" ||
			r.Header.Get("If-Unmodified-Since") != "")
}

func (h *Handler) downloadWithRanges(w http.ResponseWriter, r *http.Request, meta *datafile.File, storage downloadRangeStorage) {
	ctx := r.Context()
	stat, err := h.storage.Stat(ctx, meta.Path)
	if err != nil || stat == nil || stat.Size < 0 {
		writeFileInternalError(w, r, "failed to read file")
		return
	}

	// 用标准库判定条件和单个范围，先确定响应，再打开后端内容流。
	plan := &downloadPlan{header: make(http.Header), size: stat.Size}
	setDownloadHeaders(plan.header, meta, true)
	probe := r.WithContext(ctx)
	probe.Method = http.MethodHead
	requestedRange := probe.Header.Get("Range")
	// 多段请求返回完整表示，避免把一个请求放大为多个后端读取。
	if r.Method == http.MethodHead || !strings.HasPrefix(requestedRange, "bytes=") || strings.Contains(requestedRange, ",") {
		probe = withoutDownloadRange(probe)
	}
	http.ServeContent(privateDownloadWriter{ResponseWriter: plan}, probe, meta.Name, meta.UpdatedAt, plan)

	if r.Method == http.MethodHead || (plan.status != http.StatusOK && plan.status != http.StatusPartialContent) {
		plan.commit(w)
		if r.Method != http.MethodHead && plan.body.Len() != 0 {
			_, _ = w.Write(plan.body.Bytes())
		}
		return
	}

	length := stat.Size
	var content io.ReadCloser
	if plan.status == http.StatusPartialContent {
		length, err = strconv.ParseInt(plan.header.Get("Content-Length"), 10, 64)
		if err == nil {
			content, err = storage.GetRange(ctx, meta.Path, plan.offset, length, stat.Size)
		}
	} else {
		content, err = h.storage.Get(ctx, meta.Path)
	}
	if err != nil {
		writeFileInternalError(w, r, "failed to read file")
		return
	}
	defer func() { _ = content.Close() }()
	plan.commit(w)
	_, _ = io.CopyN(w, content, length)
}

// downloadPlan 只接收 ServeContent 的头和诊断正文。内容流在计划完成后打开。
type downloadPlan struct {
	header http.Header
	body   bytes.Buffer
	status int
	size   int64
	offset int64
}

func (p *downloadPlan) Header() http.Header         { return p.header }
func (p *downloadPlan) WriteHeader(status int)      { p.status = status }
func (p *downloadPlan) Write(b []byte) (int, error) { return p.body.Write(b) }
func (*downloadPlan) Read([]byte) (int, error)      { return 0, io.EOF }

func (p *downloadPlan) Seek(offset int64, whence int) (int64, error) {
	switch {
	case offset == 0 && whence == io.SeekEnd:
		return p.size, nil
	case whence == io.SeekStart && offset >= 0 && offset <= p.size:
		p.offset = offset
		return offset, nil
	default:
		return 0, errors.ErrUnsupported
	}
}

func (p *downloadPlan) commit(w http.ResponseWriter) {
	if p.status == http.StatusOK && w.Header().Get("Content-Encoding") != "" {
		p.header.Del("Content-Length")
	}
	for key, values := range p.header {
		w.Header()[key] = values
	}
	w.WriteHeader(p.status)
}
