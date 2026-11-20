package frontend

import (
	"fmt"
	"net/http"
	"path"
	"strings"
	"unicode"
)

const (
	defaultIndex = "index.html"
	methodAny    = "ANY"
	allowMethods = "GET, HEAD"
)

// config defines how a built frontend bundle is served.
type config struct {
	Prefix              string
	IndexFile           string
	CacheControl        string
	IndexCacheControl   string
	Fallback            bool
	Headers             map[string]string
	EnablePrecompressed bool
	PrecompressedMiss   func(PrecompressedVariantMiss)
	PrecompressedPlan   map[string]PrecompressedVariants
	NotFoundPage        string
	ErrorPage           string
	MIMETypes           map[string]string
}

// Option configures a frontend mount.
//
// The option target is intentionally package-private. Callers should compose
// the exported With* helpers rather than defining custom options against
// internal state. Future stable configuration should be added as explicit
// exported helpers so API snapshots can review each new knob.
type Option func(*config)

// WithPrefix sets the mount prefix for the frontend bundle.
func WithPrefix(prefix string) Option {
	return func(cfg *config) {
		cfg.Prefix = prefix
	}
}

// WithIndex overrides the default index.html fallback filename.
func WithIndex(name string) Option {
	return func(cfg *config) {
		cfg.IndexFile = name
	}
}

// WithCacheControl sets a Cache-Control header for served assets.
func WithCacheControl(header string) Option {
	return func(cfg *config) {
		cfg.CacheControl = header
	}
}

// WithIndexCacheControl sets the Cache-Control header for index responses.
func WithIndexCacheControl(header string) Option {
	return func(cfg *config) {
		cfg.IndexCacheControl = header
	}
}

// WithFallback enables or disables SPA fallback to the index file.
func WithFallback(enabled bool) Option {
	return func(cfg *config) {
		cfg.Fallback = enabled
	}
}

// WithHeaders applies additional headers to every successful file response.
//
// Header names are canonicalized and values are trimmed during mount
// construction. Values containing CR, LF, NUL, or other control characters are
// rejected. Transport-managed headers such as Content-Type, Content-Encoding,
// Cache-Control, Vary, range/conditional headers, and hop-by-hop headers cannot
// be set with this option.
func WithHeaders(headers map[string]string) Option {
	return func(cfg *config) {
		cfg.Headers = cloneHeaders(headers)
	}
}

// WithPrecompressed enables serving pre-compressed files (.gz, .br).
// When enabled, if a requested file has a .gz or .br variant and the client
// supports that encoding, the pre-compressed file is served directly.
//
// Directory-backed mounts index variants during construction and fail mount
// construction on scan errors. Other filesystems probe variants lazily during
// requests. Missing or unreadable variants are best-effort misses: the original
// asset is served when identity encoding is acceptable, and requests that
// refuse identity receive 406 when no accepted variant can be served.
func WithPrecompressed(enabled bool) Option {
	return func(cfg *config) {
		cfg.EnablePrecompressed = enabled
	}
}

// PrecompressedVariantMiss describes an accepted precompressed variant that
// could not be served and was treated as a best-effort miss.
type PrecompressedVariantMiss struct {
	// Path is the original asset path relative to the frontend filesystem root.
	Path string
	// VariantPath is the compressed candidate path relative to the frontend
	// filesystem root.
	VariantPath string
	// Encoding is the HTTP content encoding for the candidate, such as "br" or
	// "gzip".
	Encoding string
	// Operation is "open" when the candidate could not be opened, or "stat" when
	// the candidate opened but could not provide usable file metadata.
	Operation string
}

// WithPrecompressedVariantMissHandler observes precompressed variant downgrade
// events.
//
// The handler is called synchronously during request serving after an accepted
// planned variant cannot be opened, or after any accepted variant opens but
// cannot provide usable file metadata. The default is nil, which emits no log or
// metric and preserves best-effort downgrade behavior.
func WithPrecompressedVariantMissHandler(handler func(PrecompressedVariantMiss)) Option {
	return func(cfg *config) {
		cfg.PrecompressedMiss = handler
	}
}

// PrecompressedVariants describes which compressed files exist for an original
// asset path.
type PrecompressedVariants struct {
	Brotli bool
	Gzip   bool
}

// WithPrecompressedVariantPlan provides construction-time variant metadata for
// custom http.FileSystem implementations.
//
// Paths are original asset paths relative to the frontend filesystem root. The
// option is useful for remote, generated, or embedded filesystems where lazy
// `.br`/`.gz` probing on original responses is too expensive. The plan is used
// only when the filesystem cannot provide a directory-backed plan. Callers own
// the correctness of the supplied metadata.
func WithPrecompressedVariantPlan(variants map[string]PrecompressedVariants) Option {
	variants = clonePrecompressedVariantPlan(variants)
	return func(cfg *config) {
		cfg.PrecompressedPlan = variants
	}
}

// WithNotFoundPage sets a custom 404 error page.
// The page path is relative to the filesystem root.
func WithNotFoundPage(page string) Option {
	return func(cfg *config) {
		cfg.NotFoundPage = strings.TrimSpace(page)
	}
}

// WithErrorPage sets a custom 5xx error page.
// The page path is relative to the filesystem root.
func WithErrorPage(page string) Option {
	return func(cfg *config) {
		cfg.ErrorPage = strings.TrimSpace(page)
	}
}

// WithMIMETypes sets custom Content-Type mappings for file extensions.
// Conflicting values for the same normalized extension are rejected.
//
// Extensions may omit the leading dot; it is added automatically, and extension
// keys are lowercased. Empty values are ignored. Empty or malformed extension
// keys are rejected during mount construction. MIME values containing CR, LF,
// NUL, or other control characters are rejected during mount
// construction. The provided map is copied when the option is created, so later
// caller mutations do not affect the mount.
func WithMIMETypes(mimeTypes map[string]string) Option {
	mimeTypes = cloneMIMETypes(mimeTypes)
	return func(cfg *config) {
		cfg.MIMETypes = mimeTypes
	}
}

func newConfig(opts ...Option) (*config, error) {
	cfg := &config{
		Prefix:    "/",
		IndexFile: defaultIndex,
		Fallback:  true,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	cleanedPrefix, err := normalizePrefix(cfg.Prefix)
	if err != nil {
		return nil, fmt.Errorf("invalid prefix %q: %w", cfg.Prefix, err)
	}
	cfg.Prefix = cleanedPrefix

	indexFile := strings.TrimSpace(cfg.IndexFile)
	if indexFile == "" {
		indexFile = defaultIndex
	}
	if strings.Contains(indexFile, "/") || strings.Contains(indexFile, "\\") {
		return nil, fmt.Errorf("index file %q cannot contain path separators", cfg.IndexFile)
	}
	cfg.IndexFile = indexFile
	if cfg.NotFoundPage != "" {
		page, ok := cleanAssetPath(cfg.NotFoundPage)
		if !ok {
			return nil, fmt.Errorf("not found page %q must be a relative asset path", cfg.NotFoundPage)
		}
		cfg.NotFoundPage = page
	}
	if cfg.ErrorPage != "" {
		page, ok := cleanAssetPath(cfg.ErrorPage)
		if !ok {
			return nil, fmt.Errorf("error page %q must be a relative asset path", cfg.ErrorPage)
		}
		cfg.ErrorPage = page
	}
	cacheControl, err := normalizeHeaderOptionValue("cache control", cfg.CacheControl)
	if err != nil {
		return nil, err
	}
	cfg.CacheControl = cacheControl
	indexCacheControl, err := normalizeHeaderOptionValue("index cache control", cfg.IndexCacheControl)
	if err != nil {
		return nil, err
	}
	cfg.IndexCacheControl = indexCacheControl
	headers, err := normalizeHeaders(cfg.Headers)
	if err != nil {
		return nil, err
	}
	cfg.Headers = headers
	precompressedPlan, err := normalizePrecompressedVariantPlan(cfg.PrecompressedPlan)
	if err != nil {
		return nil, err
	}
	cfg.PrecompressedPlan = precompressedPlan
	mimeTypes, err := normalizeMIMETypes(cfg.MIMETypes)
	if err != nil {
		return nil, err
	}
	cfg.MIMETypes = mimeTypes
	return cfg, nil
}

// normalizePrefix validates and normalizes the URL prefix.
func normalizePrefix(prefix string) (string, error) {
	if prefix == "" {
		return "/", nil
	}

	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "/", nil
	}

	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}

	if strings.Contains(prefix, "\x00") || strings.Contains(prefix, "\\") {
		return "", fmt.Errorf("prefix contains path traversal elements")
	}
	if hasUnsafePathSegment(prefix) {
		return "", fmt.Errorf("prefix contains path traversal elements")
	}

	cleaned := path.Clean(prefix)
	if hasUnsafePathSegment(cleaned) {
		return "", fmt.Errorf("prefix contains path traversal elements")
	}
	cleaned = strings.TrimSuffix(cleaned, "/")
	if cleaned == "" {
		cleaned = "/"
	}

	return cleaned, nil
}

// normalizeMIMETypes normalizes MIME type extension keys: trims whitespace,
// ensures a leading dot, lowercases keys, and filters entries with empty values.
func normalizeMIMETypes(raw map[string]string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	normalized := make(map[string]string, len(raw))
	for ext, mime := range raw {
		mime = strings.TrimSpace(mime)
		if mime == "" {
			continue
		}
		normalizedExt, ok := normalizeMIMEExtension(ext)
		if !ok {
			return nil, fmt.Errorf("frontend MIME type extension %q is invalid", ext)
		}
		if !isSafeHeaderValue(mime) {
			return nil, fmt.Errorf("frontend MIME type %q contains invalid value", normalizedExt)
		}
		if existing, ok := normalized[normalizedExt]; ok && existing != mime {
			return nil, fmt.Errorf("frontend MIME type %q has conflicting values", normalizedExt)
		}
		normalized[normalizedExt] = mime
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

func normalizePrecompressedVariantPlan(raw map[string]PrecompressedVariants) (map[string]PrecompressedVariants, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	normalized := make(map[string]PrecompressedVariants, len(raw))
	for filePath, variants := range raw {
		if !variants.any() {
			continue
		}
		cleaned, ok := cleanAssetPath(filePath)
		if !ok {
			return nil, fmt.Errorf("frontend precompressed variant path %q must be a relative asset path", filePath)
		}
		normalized[cleaned] = variants
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

func normalizeMIMEExtension(ext string) (string, bool) {
	ext = strings.TrimSpace(ext)
	if ext == "" {
		return "", false
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	if ext == "." || strings.Contains(ext[1:], ".") || strings.ContainsAny(ext, `/\`) {
		return "", false
	}
	for _, r := range ext {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", false
		}
	}
	return strings.ToLower(ext), true
}

func normalizeHeaderOptionValue(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !isSafeHeaderValue(value) {
		return "", fmt.Errorf("frontend %s contains invalid value", name)
	}
	return value, nil
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	copied := make(map[string]string, len(headers))
	for key, value := range headers {
		cleanKey := http.CanonicalHeaderKey(strings.TrimSpace(key))
		if cleanKey == "" {
			continue
		}
		copied[cleanKey] = strings.TrimSpace(value)
	}
	if len(copied) == 0 {
		return nil
	}
	return copied
}

func cloneMIMETypes(mimeTypes map[string]string) map[string]string {
	if len(mimeTypes) == 0 {
		return nil
	}
	copied := make(map[string]string, len(mimeTypes))
	for key, value := range mimeTypes {
		copied[key] = value
	}
	return copied
}

func clonePrecompressedVariantPlan(variants map[string]PrecompressedVariants) map[string]PrecompressedVariants {
	if len(variants) == 0 {
		return nil
	}
	copied := make(map[string]PrecompressedVariants, len(variants))
	for key, value := range variants {
		copied[key] = value
	}
	return copied
}

func normalizeHeaders(headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	normalized := make(map[string]string, len(headers))
	for key, value := range headers {
		cleanKey := http.CanonicalHeaderKey(strings.TrimSpace(key))
		if cleanKey == "" {
			continue
		}
		if isDisallowedCustomHeader(cleanKey) {
			return nil, fmt.Errorf("frontend header %q cannot be set with WithHeaders", cleanKey)
		}
		cleanValue := strings.TrimSpace(value)
		if !isSafeHeaderValue(cleanValue) {
			return nil, fmt.Errorf("frontend header %q contains invalid value", cleanKey)
		}
		normalized[cleanKey] = cleanValue
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

func isDisallowedCustomHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Accept-Ranges",
		"Cache-Control",
		"Connection",
		"Content-Encoding",
		"Content-Length",
		"Content-Range",
		"Content-Type",
		"Etag",
		"Keep-Alive",
		"Last-Modified",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
		"Vary":
		return true
	default:
		return false
	}
}

func isSafeHeaderValue(value string) bool {
	for _, r := range value {
		if r == '\t' {
			continue
		}
		if r == '\r' || r == '\n' || r == 0 || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
