package frontend

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/birdordev/plumego/contract"
)

type handler struct {
	cfg      config
	fs       http.FileSystem
	variants precompressedVariantPlan
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", allowMethods)
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeMethodNotAllowed).
			Message("method not allowed").
			Build())
		return
	}

	rel, ok := h.stripPrefix(r.URL.Path)
	if !ok {
		h.serveNotFound(w, r)
		return
	}

	if rel == "" {
		h.serveIndex(w, r)
		return
	}

	filePath, ok := cleanAssetPath(rel)
	if !ok {
		h.serveNotFound(w, r)
		return
	}

	served, err := h.serveFile(w, r, filePath)
	if err != nil {
		h.serveError(w, r, "internal server error", http.StatusInternalServerError)
		return
	}
	if !served {
		if h.cfg.Fallback && shouldFallbackToIndex(r, filePath) {
			h.serveIndex(w, r)
		} else {
			h.serveNotFound(w, r)
		}
	}
}

// stripPrefix extracts the relative path from requestPath by stripping h.cfg.Prefix.
// Returns ("", false) if the path does not belong to this handler's prefix.
// Returns (rel, true) on success; rel may be "" meaning: serve the index.
func (h *handler) stripPrefix(requestPath string) (string, bool) {
	if h.cfg.Prefix == "/" {
		if requestPath == "/" {
			return "", true
		}
		return strings.TrimPrefix(requestPath, "/"), true
	}
	if requestPath == h.cfg.Prefix {
		return "", true
	}
	if !strings.HasPrefix(requestPath, h.cfg.Prefix+"/") {
		return "", false
	}
	return strings.TrimPrefix(requestPath, h.cfg.Prefix+"/"), true
}

// serveIndex serves the configured index file, falling back to 404 if not found.
func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	served, err := h.serveFile(w, r, h.cfg.IndexFile)
	if err != nil {
		h.serveError(w, r, "internal server error", http.StatusInternalServerError)
	} else if !served {
		h.serveNotFound(w, r)
	}
}

// serveFile attempts to serve the specified file. Return values:
//   - (true, nil):  file was served, response written
//   - (false, nil): file not found, no response written; caller should fall back or serve 404
//   - (false, err): server-side IO error, no response written; caller should call serveError
func (h *handler) serveFile(w http.ResponseWriter, r *http.Request, filePath string) (bool, error) {
	return h.serveFileWithPolicy(w, r, filePath, true)
}

func (h *handler) serveFileWithPolicy(w http.ResponseWriter, r *http.Request, filePath string, includeAssetCache bool) (bool, error) {
	cleaned, ok := cleanAssetPath(filePath)
	if !ok {
		return false, nil
	}
	filePath = cleaned

	if preFile, preStat, encoding := h.tryPlannedPrecompressed(r, filePath); preFile != nil {
		defer func() { _ = preFile.Close() }()
		h.applyFileHeaders(w, filePath, includeAssetCache)
		w.Header().Set("Content-Encoding", encoding)
		w.Header().Add("Vary", "Accept-Encoding")
		http.ServeContent(w, serveContentRequest(r, includeAssetCache), path.Base(filePath), preStat.ModTime(), preFile)
		return true, nil
	}

	f, err := h.fs.Open(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("open %q: %w", filePath, err)
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("stat %q: %w", filePath, err)
	}

	if stat.IsDir() {
		return h.serveFileWithPolicy(w, r, path.Join(filePath, h.cfg.IndexFile), includeAssetCache)
	}

	if preFile, preStat, encoding := h.tryLazyPrecompressed(r, filePath); preFile != nil {
		defer func() { _ = preFile.Close() }()
		h.applyFileHeaders(w, filePath, includeAssetCache)
		w.Header().Set("Content-Encoding", encoding)
		w.Header().Add("Vary", "Accept-Encoding")
		http.ServeContent(w, serveContentRequest(r, includeAssetCache), path.Base(filePath), preStat.ModTime(), preFile)
		return true, nil
	}
	if !identityEncodingAcceptable(r) {
		h.serveNotAcceptable(w, r)
		return true, nil
	}

	h.applyFileHeaders(w, filePath, includeAssetCache)
	if h.hasPrecompressedVariant(filePath) {
		w.Header().Add("Vary", "Accept-Encoding")
	}
	http.ServeContent(w, serveContentRequest(r, includeAssetCache), path.Base(filePath), stat.ModTime(), f)
	return true, nil
}

func serveContentRequest(r *http.Request, includeAssetCache bool) *http.Request {
	if includeAssetCache {
		return r
	}
	copied := r.Clone(r.Context())
	copied.Header = r.Header.Clone()
	for _, key := range []string{
		"Range",
		"If-Range",
		"If-Match",
		"If-None-Match",
		"If-Modified-Since",
		"If-Unmodified-Since",
	} {
		copied.Header.Del(key)
	}
	return copied
}

// applyFileHeaders sets response headers common to file responses:
// custom security/app headers, optional cache-control, and custom MIME type.
func (h *handler) applyFileHeaders(w http.ResponseWriter, filePath string, includeAssetCache bool) {
	for key, value := range h.cfg.Headers {
		if key != "" {
			w.Header().Set(key, value)
		}
	}
	if includeAssetCache {
		if isIndexFile(filePath, h.cfg.IndexFile) {
			if h.cfg.IndexCacheControl != "" {
				w.Header().Set("Cache-Control", h.cfg.IndexCacheControl)
			}
		} else if h.cfg.CacheControl != "" {
			w.Header().Set("Cache-Control", h.cfg.CacheControl)
		}
	}
	if ext := strings.ToLower(path.Ext(filePath)); ext != "" {
		if customType := h.cfg.MIMETypes[ext]; customType != "" {
			w.Header().Set("Content-Type", customType)
		}
	}
}

// isIndexFile checks if the given path is the index file.
func isIndexFile(filePath, indexFile string) bool {
	cleanPath := path.Clean(filePath)
	cleanIndex := path.Clean(indexFile)
	return cleanPath == cleanIndex || strings.HasSuffix(cleanPath, "/"+cleanIndex)
}
