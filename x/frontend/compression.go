package frontend

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// acceptsToken reports whether the client's Accept-Encoding header accepts the
// named encoding token. It compares tokens case-insensitively and rejects tokens
// with a zero quality factor.
func acceptsToken(r *http.Request, token string) bool {
	return acceptedEncodings(r.Header.Get("Accept-Encoding")).quality(token) > 0
}

type encodingPreference struct {
	headerPresent bool
	explicit      map[string]float64
	wildcard      float64
}

func acceptedEncodings(header string) encodingPreference {
	pref := encodingPreference{wildcard: -1}
	if header == "" {
		return pref
	}
	pref.headerPresent = true
	for _, part := range strings.Split(header, ",") {
		token, ok := parseWeightedToken(part)
		if !ok {
			continue
		}
		if token.value == "*" {
			pref.wildcard = token.quality
			continue
		}
		if pref.explicit == nil {
			pref.explicit = make(map[string]float64)
		}
		pref.explicit[token.value] = token.quality
	}
	return pref
}

func (p encodingPreference) quality(token string) float64 {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return 0
	}
	if q, ok := p.explicit[token]; ok {
		return q
	}
	if p.wildcard >= 0 {
		return p.wildcard
	}
	return 0
}

func (p encodingPreference) identityAcceptable() bool {
	if !p.headerPresent {
		return true
	}
	if q, ok := p.explicit["identity"]; ok {
		return q > 0
	}
	if p.wildcard >= 0 {
		return p.wildcard > 0
	}
	return true
}

// tryLazyPrecompressed attempts to serve a precompressed variant from a custom
// filesystem. Directory-backed mounts use tryPlannedPrecompressed instead so a
// missing planned variant is not retried after the original asset is opened.
func (h *handler) tryLazyPrecompressed(r *http.Request, filePath string) (http.File, os.FileInfo, string) {
	if !h.cfg.EnablePrecompressed || h.variants.known {
		return nil, nil, ""
	}

	for _, encoding := range preferredPrecompressedEncodings(r) {
		if f, stat := h.tryOpenPrecompressedVariant(filePath, encoding, false); f != nil {
			return f, stat, encoding
		}
	}

	return nil, nil, ""
}

// tryPlannedPrecompressed serves construction-time indexed variants before the
// original file is opened. It is only used for directory-backed filesystems,
// where the plan proves the original existed during mount construction.
func (h *handler) tryPlannedPrecompressed(r *http.Request, filePath string) (http.File, os.FileInfo, string) {
	if !h.cfg.EnablePrecompressed || !h.variants.known {
		return nil, nil, ""
	}
	if h.variants.requireLocalOriginal && !h.localFileExists(filePath) {
		return nil, nil, ""
	}
	variants := h.variants.variants[filePath]
	if !variants.any() {
		return nil, nil, ""
	}
	for _, encoding := range preferredPrecompressedEncodings(r) {
		if !variants.has(encoding) {
			continue
		}
		if f, stat := h.tryOpenPrecompressedVariant(filePath, encoding, true); f != nil {
			return f, stat, encoding
		}
	}
	return nil, nil, ""
}

func (h *handler) localFileExists(filePath string) bool {
	root, ok := h.fs.(localDirFS)
	if !ok {
		return false
	}
	f, err := root.Open(filePath)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	return true
}

func preferredPrecompressedEncodings(r *http.Request) []string {
	pref := acceptedEncodings(r.Header.Get("Accept-Encoding"))
	brQ := pref.quality("br")
	gzipQ := pref.quality("gzip")
	switch {
	case brQ <= 0 && gzipQ <= 0:
		return nil
	case brQ >= gzipQ:
		out := []string{}
		if brQ > 0 {
			out = append(out, "br")
		}
		if gzipQ > 0 {
			out = append(out, "gzip")
		}
		return out
	default:
		out := []string{}
		if gzipQ > 0 {
			out = append(out, "gzip")
		}
		if brQ > 0 {
			out = append(out, "br")
		}
		return out
	}
}

func identityEncodingAcceptable(r *http.Request) bool {
	return acceptedEncodings(r.Header.Get("Accept-Encoding")).identityAcceptable()
}

func (v PrecompressedVariants) has(encoding string) bool {
	switch encoding {
	case "br":
		return v.Brotli
	case "gzip":
		return v.Gzip
	default:
		return false
	}
}

func (v PrecompressedVariants) any() bool {
	return v.Brotli || v.Gzip
}

type precompressedVariantPlan struct {
	known                bool
	requireLocalOriginal bool
	variants             map[string]PrecompressedVariants
}

func newPrecompressedVariantPlan(fsys http.FileSystem, enabled bool) (precompressedVariantPlan, error) {
	if !enabled {
		return precompressedVariantPlan{}, nil
	}
	root, ok := fsys.(localDirFS)
	if !ok {
		return precompressedVariantPlan{}, nil
	}

	plan := precompressedVariantPlan{
		known:                true,
		requireLocalOriginal: true,
		variants:             make(map[string]PrecompressedVariants),
	}
	err := filepath.WalkDir(string(root), func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("scan precompressed variant %q: %w", name, err)
		}
		if entry.IsDir() {
			return nil
		}
		encoding, ok := precompressedEncodingForPath(name)
		if !ok {
			return nil
		}
		originalName := strings.TrimSuffix(name, precompressedSuffix(encoding))
		info, err := os.Stat(originalName)
		if err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(string(root), originalName)
		if err != nil {
			return fmt.Errorf("scan precompressed variant %q relative path: %w", name, err)
		}
		filePath, ok := cleanAssetPath(filepath.ToSlash(rel))
		if !ok {
			return nil
		}
		variants := plan.variants[filePath]
		switch encoding {
		case "br":
			variants.Brotli = true
		case "gzip":
			variants.Gzip = true
		}
		plan.variants[filePath] = variants
		return nil
	})
	if err != nil {
		return precompressedVariantPlan{}, err
	}
	if len(plan.variants) == 0 {
		plan.variants = nil
	}
	return plan, nil
}

func precompressedEncodingForPath(filePath string) (string, bool) {
	switch {
	case strings.HasSuffix(filePath, ".br"):
		return "br", true
	case strings.HasSuffix(filePath, ".gz"):
		return "gzip", true
	default:
		return "", false
	}
}

func precompressedSuffix(encoding string) string {
	if encoding == "gzip" {
		return ".gz"
	}
	return "." + encoding
}

func (h *handler) hasPrecompressedVariant(filePath string) bool {
	if !h.cfg.EnablePrecompressed {
		return false
	}
	if h.variants.known {
		return h.variants.variants[filePath].any()
	}
	if f, _ := h.tryOpenFile(filePath + ".br"); f != nil {
		_ = f.Close()
		return true
	}
	if f, _ := h.tryOpenFile(filePath + ".gz"); f != nil {
		_ = f.Close()
		return true
	}
	return false
}

// tryOpenFile attempts to open a file and return it with its stat.
func (h *handler) tryOpenFile(filePath string) (http.File, os.FileInfo) {
	f, err := h.fs.Open(filePath)
	if err != nil {
		return nil, nil
	}
	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		_ = f.Close()
		return nil, nil
	}
	return f, stat
}

func (h *handler) tryOpenPrecompressedVariant(filePath, encoding string, reportOpenMiss bool) (http.File, os.FileInfo) {
	variantPath := filePath + precompressedSuffix(encoding)
	f, err := h.fs.Open(variantPath)
	if err != nil {
		if reportOpenMiss {
			h.reportPrecompressedMiss(filePath, variantPath, encoding, "open")
		}
		return nil, nil
	}
	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		_ = f.Close()
		h.reportPrecompressedMiss(filePath, variantPath, encoding, "stat")
		return nil, nil
	}
	return f, stat
}

func (h *handler) reportPrecompressedMiss(filePath, variantPath, encoding, operation string) {
	if h.cfg.PrecompressedMiss == nil {
		return
	}
	h.cfg.PrecompressedMiss(PrecompressedVariantMiss{
		Path:        filePath,
		VariantPath: variantPath,
		Encoding:    encoding,
		Operation:   operation,
	})
}
