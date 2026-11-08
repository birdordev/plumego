// Package headers provides HTTP security header policy primitives.
//
// This package implements a comprehensive security header policy system supporting:
//   - HSTS (HTTP Strict Transport Security)
//   - CSP (Content Security Policy)
//   - X-Frame-Options
//   - X-Content-Type-Options
//   - Referrer-Policy
//   - Permissions-Policy
//
// Features:
//   - Declarative policy configuration
//   - header application primitives for transport adapters
//   - Policy validation and error reporting
//
// Example usage:
//
//	import (
//		"github.com/birdordev/plumego/middleware/securityheaders"
//		"github.com/birdordev/plumego/security/headers"
//	)
//
//	policy := headers.Policy{
//		StrictTransportSecurity: &headers.HSTSOptions{
//			MaxAge:            365 * 24 * time.Hour,
//			IncludeSubDomains: true,
//			Preload:           true,
//		},
//		FrameOptions:       "DENY",
//		ContentTypeOptions: "nosniff",
//		ContentSecurityPolicy: headers.NewCSPBuilder().
//			DefaultSrc("'self'").
//			ScriptSrc("'self'").
//			Build(),
//	}
//
//	// Apply with the canonical transport adapter
//	mw, err := securityheaders.Middleware(securityheaders.Config{Policy: &policy})
//	if err != nil {
//		return err
//	}
//	app.Use(mw)
package headers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/birdordev/plumego/security/input"
)

var ErrInvalidPolicy = errors.New("headers: invalid policy")

// HSTSOptions configures Strict-Transport-Security.
//
// HSTS (HTTP Strict Transport Security) forces browsers to use HTTPS for all
// future requests to the domain, preventing SSL stripping attacks.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/headers"
//
//	hsts := headers.HSTSOptions{
//		MaxAge:            365 * 24 * time.Hour, // 1 year
//		IncludeSubDomains: true,
//		Preload:           true,
//	}
//	policy := headers.Policy{
//		StrictTransportSecurity: &hsts,
//	}
type HSTSOptions struct {
	// MaxAge is the duration (in seconds) that the browser should remember
	// to only use HTTPS for this domain
	MaxAge time.Duration

	// IncludeSubDomains indicates whether the HSTS policy applies to all subdomains
	IncludeSubDomains bool

	// Preload indicates whether the domain can be included in browser HSTS preload lists
	Preload bool
}

// Policy defines security headers that should be applied to responses.
//
// Policy configures various security headers to protect against common web
// vulnerabilities such as clickjacking, XSS, MIME sniffing, and more.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/headers"
//
//	policy := headers.Policy{
//		FrameOptions:          "DENY",
//		ContentTypeOptions:    "nosniff",
//		ReferrerPolicy:        "strict-origin-when-cross-origin",
//		ContentSecurityPolicy: "default-src 'self'",
//	}
//
// Direct callers that need fail-closed policy handling should use ApplyChecked:
//
//	if err := policy.ApplyChecked(w, r); err != nil {
//		// reject startup or request handling
//	}
type Policy struct {
	// FrameOptions controls whether the page can be displayed in a frame
	// Values: DENY, SAMEORIGIN
	FrameOptions string

	// ContentTypeOptions prevents MIME sniffing attacks
	// Values: nosniff
	ContentTypeOptions string

	// ReferrerPolicy controls how much referrer information is sent
	// Values: no-referrer, strict-origin-when-cross-origin, etc.
	ReferrerPolicy string

	// PermissionsPolicy controls browser features and capabilities
	// Values: geolocation=(), camera=(), microphone=(), etc.
	PermissionsPolicy string

	// ContentSecurityPolicy controls which resources can be loaded
	// Values: default-src 'self', script-src 'self' 'unsafe-inline', etc.
	ContentSecurityPolicy string

	// CrossOriginOpenerPolicy controls cross-origin opener isolation
	// Values: same-origin, same-origin-allow-popups, unsafe-none
	CrossOriginOpenerPolicy string

	// CrossOriginResourcePolicy controls which resources can be loaded
	// Values: same-site, same-origin, cross-origin
	CrossOriginResourcePolicy string

	// CrossOriginEmbedderPolicy controls cross-origin embedding
	// Values: require-corp, unsafe-none
	CrossOriginEmbedderPolicy string

	// StrictTransportSecurity configures HSTS
	StrictTransportSecurity *HSTSOptions

	// Additional allows custom security headers
	Additional map[string]string

	// compiledHSTS holds the precomputed Strict-Transport-Security header value.
	// Populated by Compile; zero means "compute on demand".
	compiledHSTS string
}

// DefaultPolicy returns a baseline header policy with safe defaults.
//
// The default policy includes:
//   - X-Frame-Options: SAMEORIGIN (prevents clickjacking)
//   - X-Content-Type-Options: nosniff (prevents MIME sniffing)
//   - Referrer-Policy: strict-origin-when-cross-origin
//
// Example:
//
//	import "github.com/birdordev/plumego/security/headers"
//
//	policy := headers.DefaultPolicy()
//	if err := policy.ApplyChecked(w, r); err != nil {
//		// reject startup or request handling
//	}
func DefaultPolicy() Policy {
	return Policy{
		FrameOptions:       "SAMEORIGIN",
		ContentTypeOptions: "nosniff",
		ReferrerPolicy:     "strict-origin-when-cross-origin",
	}
}

// Compile precomputes invariant header values (currently HSTS) and returns an
// optimised copy of the policy. The original is not modified.
//
// Call Compile once after the policy is fully configured, typically inside the
// transport middleware constructor. Uncompiled policies still produce correct
// output; Compile only avoids redundant string allocation per request.
func (p Policy) Compile() Policy {
	if p.StrictTransportSecurity != nil && p.StrictTransportSecurity.MaxAge > 0 {
		p.compiledHSTS = formatHSTS(*p.StrictTransportSecurity)
	}
	return p
}

// StrictPolicy returns a hardened policy suitable for locked-down deployments.
//
// The strict policy includes:
//   - X-Frame-Options: DENY (prevents all framing)
//   - X-Content-Type-Options: nosniff
//   - Referrer-Policy: no-referrer
//   - Permissions-Policy: geolocation=(), camera=(), microphone=()
//   - Content-Security-Policy: strict CSP (via StrictCSP)
//   - Cross-Origin-Opener-Policy: same-origin
//   - Cross-Origin-Resource-Policy: same-origin
//   - Cross-Origin-Embedder-Policy: require-corp
//   - Strict-Transport-Security: max-age=31536000; includeSubDomains; preload
//
// Example:
//
//	import "github.com/birdordev/plumego/security/headers"
//
//	policy := headers.StrictPolicy()
//	if err := policy.ApplyChecked(w, r); err != nil {
//		// reject startup or request handling
//	}
func StrictPolicy() Policy {
	return Policy{
		FrameOptions:              "DENY",
		ContentTypeOptions:        "nosniff",
		ReferrerPolicy:            "no-referrer",
		PermissionsPolicy:         "geolocation=(), camera=(), microphone=()",
		CrossOriginOpenerPolicy:   "same-origin",
		CrossOriginResourcePolicy: "same-origin",
		CrossOriginEmbedderPolicy: "require-corp",
		ContentSecurityPolicy:     StrictCSP(),
		StrictTransportSecurity: &HSTSOptions{
			MaxAge:            365 * 24 * time.Hour,
			IncludeSubDomains: true,
			Preload:           true,
		},
	}
}

// Apply validates and attaches the configured headers to the response.
//
// Apply fails closed: if policy validation fails, no headers are written. Use
// ApplyChecked when the caller needs the validation error.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/headers"
//
//	policy := headers.DefaultPolicy()
//	policy.Apply(w, r)
func (p Policy) Apply(w http.ResponseWriter, r *http.Request) {
	if p.Validate() != nil {
		return
	}
	p.apply(w, r)
}

// ApplyChecked validates and attaches the configured headers to the response.
//
// ApplyChecked is the canonical direct application path when callers need an
// explicit error for invalid policy configuration. No headers are written when
// validation fails.
func (p Policy) ApplyChecked(w http.ResponseWriter, r *http.Request) error {
	if err := p.Validate(); err != nil {
		return err
	}
	p.apply(w, r)
	return nil
}

func (p Policy) apply(w http.ResponseWriter, r *http.Request) {
	headers := w.Header()
	setEnumHeader(headers, "X-Frame-Options", p.FrameOptions, allowedFrameOptions)
	setEnumHeader(headers, "X-Content-Type-Options", p.ContentTypeOptions, allowedContentTypeOptions)
	setEnumHeader(headers, "Referrer-Policy", p.ReferrerPolicy, allowedReferrerPolicies)
	setHeader(headers, "Permissions-Policy", p.PermissionsPolicy)
	setHeader(headers, "Content-Security-Policy", p.ContentSecurityPolicy)
	setEnumHeader(headers, "Cross-Origin-Opener-Policy", p.CrossOriginOpenerPolicy, allowedCrossOriginOpenerPolicies)
	setEnumHeader(headers, "Cross-Origin-Resource-Policy", p.CrossOriginResourcePolicy, allowedCrossOriginResourcePolicies)
	setEnumHeader(headers, "Cross-Origin-Embedder-Policy", p.CrossOriginEmbedderPolicy, allowedCrossOriginEmbedderPolicies)

	if p.StrictTransportSecurity != nil && p.StrictTransportSecurity.MaxAge > 0 && isHTTPSRequest(r) {
		hsts := p.compiledHSTS
		if hsts == "" {
			hsts = formatHSTS(*p.StrictTransportSecurity)
		}
		setHeader(headers, "Strict-Transport-Security", hsts)
	}

	for name, value := range p.Additional {
		if !input.IsHeaderName(name) {
			continue
		}
		setHeader(headers, http.CanonicalHeaderKey(name), value)
	}
}

func setEnumHeader(headers http.Header, name, value string, allowed map[string]struct{}) {
	if value == "" {
		return
	}
	if _, ok := allowed[strings.ToLower(strings.TrimSpace(value))]; !ok {
		return
	}
	setHeader(headers, name, value)
}

// Validate checks whether configured header policy values are safe to apply.
func (p Policy) Validate() error {
	var errs []error
	checkValue := func(name, value string) {
		if value == "" {
			return
		}
		if !input.IsHeaderValue(value) {
			errs = append(errs, fmt.Errorf("%w: %s has unsafe value", ErrInvalidPolicy, name))
		}
	}
	checkEnum := func(name, value string, allowed map[string]struct{}) {
		if value == "" {
			return
		}
		checkValue(name, value)
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(value))]; !ok {
			errs = append(errs, fmt.Errorf("%w: %s has unsupported value %q", ErrInvalidPolicy, name, value))
		}
	}

	checkEnum("X-Frame-Options", p.FrameOptions, allowedFrameOptions)
	checkEnum("X-Content-Type-Options", p.ContentTypeOptions, allowedContentTypeOptions)
	checkEnum("Referrer-Policy", p.ReferrerPolicy, allowedReferrerPolicies)
	checkValue("Permissions-Policy", p.PermissionsPolicy)
	checkValue("Content-Security-Policy", p.ContentSecurityPolicy)
	checkEnum("Cross-Origin-Opener-Policy", p.CrossOriginOpenerPolicy, allowedCrossOriginOpenerPolicies)
	checkEnum("Cross-Origin-Resource-Policy", p.CrossOriginResourcePolicy, allowedCrossOriginResourcePolicies)
	checkEnum("Cross-Origin-Embedder-Policy", p.CrossOriginEmbedderPolicy, allowedCrossOriginEmbedderPolicies)

	if p.StrictTransportSecurity != nil && p.StrictTransportSecurity.MaxAge < 0 {
		errs = append(errs, fmt.Errorf("%w: Strict-Transport-Security max age cannot be negative", ErrInvalidPolicy))
	}
	for name, value := range p.Additional {
		if !input.IsHeaderName(name) {
			errs = append(errs, fmt.Errorf("%w: additional header %q has unsafe name", ErrInvalidPolicy, name))
			continue
		}
		checkValue(http.CanonicalHeaderKey(name), value)
	}

	return errors.Join(errs...)
}

var (
	allowedFrameOptions = map[string]struct{}{
		"deny":       {},
		"sameorigin": {},
	}
	allowedContentTypeOptions = map[string]struct{}{
		"nosniff": {},
	}
	allowedReferrerPolicies = map[string]struct{}{
		"no-referrer":                     {},
		"no-referrer-when-downgrade":      {},
		"origin":                          {},
		"origin-when-cross-origin":        {},
		"same-origin":                     {},
		"strict-origin":                   {},
		"strict-origin-when-cross-origin": {},
		"unsafe-url":                      {},
	}
	allowedCrossOriginOpenerPolicies = map[string]struct{}{
		"same-origin":              {},
		"same-origin-allow-popups": {},
		"unsafe-none":              {},
	}
	allowedCrossOriginResourcePolicies = map[string]struct{}{
		"same-origin":  {},
		"same-site":    {},
		"cross-origin": {},
	}
	allowedCrossOriginEmbedderPolicies = map[string]struct{}{
		"require-corp":   {},
		"credentialless": {},
		"unsafe-none":    {},
	}
)

func setHeader(headers http.Header, name, value string) {
	if value == "" {
		return
	}
	if !input.IsHeaderValue(value) {
		return
	}
	headers.Set(name, value)
}

func formatHSTS(opts HSTSOptions) string {
	maxAge := int64(opts.MaxAge.Seconds())
	if maxAge < 0 {
		maxAge = 0
	}

	parts := []string{fmt.Sprintf("max-age=%d", maxAge)}
	if opts.IncludeSubDomains {
		parts = append(parts, "includeSubDomains")
	}
	if opts.Preload {
		parts = append(parts, "preload")
	}
	return strings.Join(parts, "; ")
}

func isHTTPSRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}

	protoHeader := r.Header.Get("X-Forwarded-Proto")
	if protoHeader != "" {
		return forwardedProtoIsHTTPS(protoHeader)
	}

	return forwardedHeaderIsHTTPS(r.Header.Get("Forwarded"))
}

func forwardedProtoIsHTTPS(value string) bool {
	parts := strings.Split(value, ",")
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !strings.EqualFold(strings.TrimSpace(part), "https") {
			return false
		}
	}
	return true
}

func forwardedHeaderIsHTTPS(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, element := range strings.Split(value, ",") {
		proto, ok := forwardedProtoParam(element)
		if !ok || !strings.EqualFold(proto, "https") {
			return false
		}
	}
	return true
}

func forwardedProtoParam(element string) (string, bool) {
	for _, param := range strings.Split(element, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(name), "proto") {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"`), true
	}
	return "", false
}

// CSPBuilder helps construct Content-Security-Policy headers.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/headers"
//
//	csp := headers.NewCSPBuilder().
//		DefaultSrc("'self'").
//		ScriptSrc("'self'", "'unsafe-inline'", "https://cdn.example.com").
//		StyleSrc("'self'", "'unsafe-inline'").
//		ImgSrc("'self'", "data:", "https:").
//		Build()
type CSPBuilder struct {
	directives map[string][]string
	invalid    map[string][]string
}

// NewCSPBuilder creates a new CSP builder.
func NewCSPBuilder() *CSPBuilder {
	return &CSPBuilder{
		directives: make(map[string][]string),
		invalid:    make(map[string][]string),
	}
}

// DefaultSrc sets the default-src directive.
func (b *CSPBuilder) DefaultSrc(sources ...string) *CSPBuilder {
	return b.setDirective("default-src", sources...)
}

// ScriptSrc sets the script-src directive.
func (b *CSPBuilder) ScriptSrc(sources ...string) *CSPBuilder {
	return b.setDirective("script-src", sources...)
}

// StyleSrc sets the style-src directive.
func (b *CSPBuilder) StyleSrc(sources ...string) *CSPBuilder {
	return b.setDirective("style-src", sources...)
}

// ImgSrc sets the img-src directive.
func (b *CSPBuilder) ImgSrc(sources ...string) *CSPBuilder {
	return b.setDirective("img-src", sources...)
}

// FontSrc sets the font-src directive.
func (b *CSPBuilder) FontSrc(sources ...string) *CSPBuilder {
	return b.setDirective("font-src", sources...)
}

// ConnectSrc sets the connect-src directive.
func (b *CSPBuilder) ConnectSrc(sources ...string) *CSPBuilder {
	return b.setDirective("connect-src", sources...)
}

// FrameSrc sets the frame-src directive.
func (b *CSPBuilder) FrameSrc(sources ...string) *CSPBuilder {
	return b.setDirective("frame-src", sources...)
}

// ObjectSrc sets the object-src directive.
func (b *CSPBuilder) ObjectSrc(sources ...string) *CSPBuilder {
	return b.setDirective("object-src", sources...)
}

// MediaSrc sets the media-src directive.
func (b *CSPBuilder) MediaSrc(sources ...string) *CSPBuilder {
	return b.setDirective("media-src", sources...)
}

// ChildSrc sets the child-src directive.
func (b *CSPBuilder) ChildSrc(sources ...string) *CSPBuilder {
	return b.setDirective("child-src", sources...)
}

// FormAction sets the form-action directive.
func (b *CSPBuilder) FormAction(sources ...string) *CSPBuilder {
	return b.setDirective("form-action", sources...)
}

// FrameAncestors sets the frame-ancestors directive.
func (b *CSPBuilder) FrameAncestors(sources ...string) *CSPBuilder {
	return b.setDirective("frame-ancestors", sources...)
}

// BaseURI sets the base-uri directive.
func (b *CSPBuilder) BaseURI(sources ...string) *CSPBuilder {
	return b.setDirective("base-uri", sources...)
}

// ManifestSrc sets the manifest-src directive.
func (b *CSPBuilder) ManifestSrc(sources ...string) *CSPBuilder {
	return b.setDirective("manifest-src", sources...)
}

// WorkerSrc sets the worker-src directive.
func (b *CSPBuilder) WorkerSrc(sources ...string) *CSPBuilder {
	return b.setDirective("worker-src", sources...)
}

// ReportURI sets the report-uri directive.
func (b *CSPBuilder) ReportURI(uri string) *CSPBuilder {
	return b.setDirective("report-uri", uri)
}

// ReportTo sets the report-to directive.
func (b *CSPBuilder) ReportTo(group string) *CSPBuilder {
	return b.setDirective("report-to", group)
}

// UpgradeInsecureRequests adds the upgrade-insecure-requests directive.
func (b *CSPBuilder) UpgradeInsecureRequests() *CSPBuilder {
	return b.setFlagDirective("upgrade-insecure-requests")
}

// BlockAllMixedContent adds the block-all-mixed-content directive.
func (b *CSPBuilder) BlockAllMixedContent() *CSPBuilder {
	return b.setFlagDirective("block-all-mixed-content")
}

// Sandbox adds the sandbox directive.
func (b *CSPBuilder) Sandbox(values ...string) *CSPBuilder {
	if len(values) == 0 {
		return b.setFlagDirective("sandbox")
	}
	return b.setDirective("sandbox", values...)
}

// Build validates and constructs the CSP header value.
//
// Build fails closed: if any source value is invalid, it returns an empty
// header value. Use BuildChecked when the caller needs the validation error.
func (b *CSPBuilder) Build() string {
	if b == nil {
		return ""
	}
	if b.Validate() != nil {
		return ""
	}
	if len(b.directives) == 0 {
		return ""
	}

	parts := make([]string, 0, len(b.directives))

	// Maintain consistent order for testing
	directiveOrder := []string{
		"default-src", "script-src", "style-src", "img-src", "font-src",
		"connect-src", "frame-src", "object-src", "media-src", "child-src",
		"form-action", "frame-ancestors", "base-uri", "manifest-src", "worker-src",
		"report-uri", "report-to", "upgrade-insecure-requests", "block-all-mixed-content",
		"sandbox",
	}

	for _, directive := range directiveOrder {
		sources, ok := b.directives[directive]
		if !ok {
			continue
		}

		if len(sources) == 0 {
			parts = append(parts, directive)
		} else {
			parts = append(parts, directive+" "+strings.Join(sources, " "))
		}
	}

	return strings.Join(parts, "; ")
}

// BuildChecked validates and constructs the CSP header value.
//
// BuildChecked returns ErrInvalidPolicy when any non-empty source value was
// rejected during builder configuration.
func (b *CSPBuilder) BuildChecked() (string, error) {
	if err := b.Validate(); err != nil {
		return "", err
	}
	return b.Build(), nil
}

// Validate reports whether the builder contains rejected source values.
func (b *CSPBuilder) Validate() error {
	if b == nil || len(b.invalid) == 0 {
		return nil
	}
	var errs []error
	for directive, values := range b.invalid {
		for _, value := range values {
			errs = append(errs, fmt.Errorf("%w: CSP %s has unsafe source %q", ErrInvalidPolicy, directive, value))
		}
	}
	return errors.Join(errs...)
}

func (b *CSPBuilder) setDirective(name string, values ...string) *CSPBuilder {
	if b == nil {
		return b
	}
	b.ensureMaps()
	cleaned := make([]string, 0, len(values))
	invalid := make([]string, 0)
	for _, value := range values {
		original := value
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !isCSPDirectiveValue(value) {
			invalid = append(invalid, original)
			continue
		}
		cleaned = append(cleaned, value)
	}
	if len(invalid) > 0 {
		b.invalid[name] = invalid
	} else {
		delete(b.invalid, name)
	}
	if len(cleaned) == 0 {
		delete(b.directives, name)
		return b
	}
	b.directives[name] = cleaned
	return b
}

func (b *CSPBuilder) setFlagDirective(name string) *CSPBuilder {
	if b == nil {
		return b
	}
	b.ensureMaps()
	b.directives[name] = []string{}
	delete(b.invalid, name)
	return b
}

func (b *CSPBuilder) ensureMaps() {
	if b.directives == nil {
		b.directives = make(map[string][]string)
	}
	if b.invalid == nil {
		b.invalid = make(map[string][]string)
	}
}

func isCSPDirectiveValue(value string) bool {
	return input.IsHeaderValue(value) && !strings.Contains(value, ";")
}

// StrictCSP returns a strict CSP policy suitable for modern applications.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/headers"
//
//	csp := headers.StrictCSP()
func StrictCSP() string {
	return NewCSPBuilder().
		DefaultSrc("'self'").
		ScriptSrc("'self'").
		StyleSrc("'self'").
		ImgSrc("'self'", "data:", "https:").
		FontSrc("'self'").
		ConnectSrc("'self'").
		FrameSrc("'none'").
		ObjectSrc("'none'").
		BaseURI("'self'").
		FormAction("'self'").
		FrameAncestors("'none'").
		UpgradeInsecureRequests().
		Build()
}
