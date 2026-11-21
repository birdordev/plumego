package headers

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompileDetachesHSTSOptions(t *testing.T) {
	options := HSTSOptions{MaxAge: time.Hour, IncludeSubDomains: true, Preload: true}
	policy := Policy{StrictTransportSecurity: &options}.Compile()
	options = HSTSOptions{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	if err := policy.ApplyChecked(w, req); err != nil {
		t.Fatal(err)
	}
	if got := w.Result().Header.Get("Strict-Transport-Security"); got != "max-age=3600; includeSubDomains; preload" {
		t.Fatalf("HSTS = %q", got)
	}
}

func TestDefaultPolicy(t *testing.T) {
	policy := DefaultPolicy()
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	w := httptest.NewRecorder()

	policy.Apply(w, req)

	resp := w.Result()
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options nosniff, got %q", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Fatalf("expected X-Frame-Options SAMEORIGIN, got %q", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got == "" {
		t.Fatalf("expected Referrer-Policy to be set")
	}
}

func TestStrictPolicyHSTS(t *testing.T) {
	policy := StrictPolicy()

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	w := httptest.NewRecorder()
	policy.Apply(w, req)
	if got := w.Result().Header.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("expected no HSTS on non-TLS request, got %q", got)
	}
	if got := w.Result().Header.Get("Content-Security-Policy"); got == "" {
		t.Fatalf("expected CSP to be set for strict policy")
	}

	tlsReq := httptest.NewRequest(http.MethodGet, "https://example.com", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	w = httptest.NewRecorder()
	policy.Apply(w, tlsReq)
	if got := w.Result().Header.Get("Strict-Transport-Security"); got == "" {
		t.Fatalf("expected HSTS on TLS request")
	}
}

func TestStrictPolicyHSTSWithForwardedProtocols(t *testing.T) {
	policy := StrictPolicy().Compile()
	tests := []struct {
		name      string
		proto     string
		forwarded string
		wantHSTS  bool
	}{
		{name: "forwarded proto HTTPS", proto: " https, HTTPS ", wantHSTS: true},
		{name: "forwarded proto mixed", proto: "https, http"},
		{name: "forwarded HTTPS", forwarded: `for=192.0.2.1; proto="https", for=192.0.2.2; proto=https`, wantHSTS: true},
		{name: "forwarded mixed", forwarded: "proto=https, proto=http"},
		{name: "forwarded missing protocol", forwarded: "for=192.0.2.1"},
		{name: "no forwarded headers"},
		{name: "forwarded proto takes precedence", proto: "http", forwarded: "proto=https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://example.test", nil)
			if tt.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tt.proto)
			}
			if tt.forwarded != "" {
				req.Header.Set("Forwarded", tt.forwarded)
			}
			rec := httptest.NewRecorder()
			if err := policy.ApplyChecked(rec, req); err != nil {
				t.Fatal(err)
			}
			if got := rec.Header().Get("Strict-Transport-Security") != ""; got != tt.wantHSTS {
				t.Fatalf("HSTS present = %t, want %t", got, tt.wantHSTS)
			}
		})
	}
}

func TestStrictPolicyDoesNotTrustForwardedSSLForHSTS(t *testing.T) {
	policy := StrictPolicy()

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("X-Forwarded-Ssl", "on")
	w := httptest.NewRecorder()

	policy.Apply(w, req)

	if got := w.Result().Header.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("expected no HSTS from X-Forwarded-Ssl alone, got %q", got)
	}
}

func TestPolicyApplyFailsClosedForInvalidAdditionalHeaders(t *testing.T) {
	policy := Policy{
		Additional: map[string]string{
			"X-Test":       "ok",
			"Bad Header":   "nope",
			"X-Injected":   "bad\nvalue",
			"X-Also-Valid": "still-ok",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	w := httptest.NewRecorder()
	policy.Apply(w, req)

	resp := w.Result()
	if got := resp.Header.Get("X-Test"); got != "" {
		t.Fatalf("expected X-Test not to be written for invalid policy, got %q", got)
	}
	if got := resp.Header.Get("Bad Header"); got != "" {
		t.Fatalf("expected invalid header name to be skipped")
	}
	if got := resp.Header.Get("X-Injected"); got != "" {
		t.Fatalf("expected invalid header value to be skipped")
	}
	if got := resp.Header.Get("X-Also-Valid"); got != "" {
		t.Fatalf("expected X-Also-Valid not to be written for invalid policy, got %q", got)
	}
}

func TestPolicyApplyFailsClosedForUnsupportedStandardHeaderValues(t *testing.T) {
	policy := Policy{
		FrameOptions:              "ALLOWALL",
		ContentTypeOptions:        "sniff",
		ReferrerPolicy:            "send-everything",
		PermissionsPolicy:         "geolocation=()",
		ContentSecurityPolicy:     "default-src 'self'",
		CrossOriginOpenerPolicy:   "isolated",
		CrossOriginResourcePolicy: "private",
		CrossOriginEmbedderPolicy: "required",
		Additional: map[string]string{
			"X-Trace-Mode": "sampled",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	w := httptest.NewRecorder()
	policy.Apply(w, req)

	resp := w.Result()
	for _, name := range []string{
		"X-Frame-Options",
		"X-Content-Type-Options",
		"Referrer-Policy",
		"Permissions-Policy",
		"Content-Security-Policy",
		"Cross-Origin-Opener-Policy",
		"Cross-Origin-Resource-Policy",
		"Cross-Origin-Embedder-Policy",
		"X-Trace-Mode",
	} {
		if got := resp.Header.Get(name); got != "" {
			t.Fatalf("expected %s not to be written for invalid policy, got %q", name, got)
		}
	}
}

func TestPolicyApplyCheckedRejectsInvalidPolicyWithoutWritingHeaders(t *testing.T) {
	policy := DefaultPolicy()
	policy.FrameOptions = "ALLOWALL"
	policy.Additional = map[string]string{"X-Trace-Mode": "sampled"}

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	w := httptest.NewRecorder()

	err := policy.ApplyChecked(w, req)
	if !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("ApplyChecked error = %v, want ErrInvalidPolicy", err)
	}
	if got := w.Result().Header.Get("X-Trace-Mode"); got != "" {
		t.Fatalf("expected no headers to be written on invalid policy, got X-Trace-Mode=%q", got)
	}
	if got := w.Result().Header.Get("X-Content-Type-Options"); got != "" {
		t.Fatalf("expected default headers not to be written on invalid policy, got %q", got)
	}
}

func TestPolicyValidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		policy := StrictPolicy()
		policy.Additional = map[string]string{"X-Trace-Mode": "sampled"}
		if err := policy.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	t.Run("invalid values", func(t *testing.T) {
		policy := Policy{
			FrameOptions:              "DENY\nX-Injected: yes",
			ContentSecurityPolicy:     "default-src 'self'\r\nscript-src *",
			StrictTransportSecurity:   &HSTSOptions{MaxAge: -1 * time.Second},
			CrossOriginResourcePolicy: "same-origin",
			Additional: map[string]string{
				"Bad Header": "value",
				"X-Unsafe":   "bad\nvalue",
			},
		}

		err := policy.Validate()
		if !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("Validate error = %v, want ErrInvalidPolicy", err)
		}
		message := err.Error()
		for _, want := range []string{
			"X-Frame-Options",
			"Content-Security-Policy",
			"Strict-Transport-Security",
			"Bad Header",
			"X-Unsafe",
		} {
			if !strings.Contains(message, want) {
				t.Fatalf("Validate error %q missing %q", message, want)
			}
		}
	})

	t.Run("invalid standard header semantics", func(t *testing.T) {
		policy := Policy{
			FrameOptions:              "ALLOWALL",
			ContentTypeOptions:        "sniff",
			ReferrerPolicy:            "send-everything",
			CrossOriginOpenerPolicy:   "isolated",
			CrossOriginResourcePolicy: "private",
			CrossOriginEmbedderPolicy: "required",
		}

		err := policy.Validate()
		if !errors.Is(err, ErrInvalidPolicy) {
			t.Fatalf("Validate error = %v, want ErrInvalidPolicy", err)
		}
		message := err.Error()
		for _, want := range []string{
			"X-Frame-Options",
			"X-Content-Type-Options",
			"Referrer-Policy",
			"Cross-Origin-Opener-Policy",
			"Cross-Origin-Resource-Policy",
			"Cross-Origin-Embedder-Policy",
		} {
			if !strings.Contains(message, want) {
				t.Fatalf("Validate error %q missing %q", message, want)
			}
		}
	})

	t.Run("valid standard header semantics are case insensitive", func(t *testing.T) {
		policy := Policy{
			FrameOptions:              "deny",
			ContentTypeOptions:        "NoSniff",
			ReferrerPolicy:            "Strict-Origin",
			CrossOriginOpenerPolicy:   "Same-Origin-Allow-Popups",
			CrossOriginResourcePolicy: "Cross-Origin",
			CrossOriginEmbedderPolicy: "Credentialless",
		}
		if err := policy.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})
}

func TestCSPBuilder(t *testing.T) {
	tests := []struct {
		name     string
		build    func() string
		expected string
	}{
		{
			name: "simple CSP",
			build: func() string {
				return NewCSPBuilder().
					DefaultSrc("'self'").
					Build()
			},
			expected: "default-src 'self'",
		},
		{
			name: "multiple directives",
			build: func() string {
				return NewCSPBuilder().
					DefaultSrc("'self'").
					ScriptSrc("'self'", "https://cdn.example.com").
					StyleSrc("'self'", "'unsafe-inline'").
					Build()
			},
			expected: "default-src 'self'; script-src 'self' https://cdn.example.com; style-src 'self' 'unsafe-inline'",
		},
		{
			name: "directive without sources",
			build: func() string {
				return NewCSPBuilder().
					DefaultSrc("'self'").
					UpgradeInsecureRequests().
					Build()
			},
			expected: "default-src 'self'; upgrade-insecure-requests",
		},
		{
			name: "frame ancestors",
			build: func() string {
				return NewCSPBuilder().
					FrameAncestors("'none'").
					Build()
			},
			expected: "frame-ancestors 'none'",
		},
		{
			name: "sandbox",
			build: func() string {
				return NewCSPBuilder().
					Sandbox("allow-scripts", "allow-forms").
					Build()
			},
			expected: "sandbox allow-scripts allow-forms",
		},
		{
			name: "bare sandbox",
			build: func() string {
				return NewCSPBuilder().
					DefaultSrc("'self'").
					Sandbox().
					Build()
			},
			expected: "default-src 'self'; sandbox",
		},
		{
			name: "report-uri",
			build: func() string {
				return NewCSPBuilder().
					DefaultSrc("'self'").
					ReportURI("/csp-report").
					Build()
			},
			expected: "default-src 'self'; report-uri /csp-report",
		},
		{
			name:     "empty builder",
			build:    func() string { return NewCSPBuilder().Build() },
			expected: "",
		},
		{
			name: "fails closed on directive injection values",
			build: func() string {
				return NewCSPBuilder().
					DefaultSrc("'self'", "'none'; script-src *").
					ScriptSrc("https://cdn.example.com", "bad\nvalue").
					ReportURI("/csp-report").
					Sandbox("allow-scripts", "allow-forms; allow-same-origin").
					Build()
			},
			expected: "",
		},
		{
			name: "fails closed on directive with only unsafe values",
			build: func() string {
				return NewCSPBuilder().
					DefaultSrc("'none'; script-src *").
					ReportURI("/bad; report-to x").
					UpgradeInsecureRequests().
					Build()
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.build()
			if got != tt.expected {
				t.Errorf("CSP mismatch\ngot:  %q\nwant: %q", got, tt.expected)
			}
		})
	}

}

func TestStrictCSP(t *testing.T) {
	csp := StrictCSP()

	if csp == "" {
		t.Fatal("StrictCSP() returned empty string")
	}

	// Check for key directives
	if !containsDirective(csp, "default-src 'self'") {
		t.Error("expected default-src 'self'")
	}
	if !containsDirective(csp, "frame-src 'none'") {
		t.Error("expected frame-src 'none'")
	}
	if !containsDirective(csp, "object-src 'none'") {
		t.Error("expected object-src 'none'")
	}
	if !containsDirective(csp, "upgrade-insecure-requests") {
		t.Error("expected upgrade-insecure-requests")
	}
}

func TestCSPBuilderBuildChecked(t *testing.T) {
	csp, err := NewCSPBuilder().
		Sandbox("allow-scripts").
		DefaultSrc("'self'").
		UpgradeInsecureRequests().
		ScriptSrc("'self'", "https://cdn.example.com").
		BuildChecked()
	if err != nil {
		t.Fatalf("BuildChecked valid CSP: %v", err)
	}
	want := "default-src 'self'; script-src 'self' https://cdn.example.com; upgrade-insecure-requests; sandbox allow-scripts"
	if csp != want {
		t.Fatalf("BuildChecked CSP = %q, want %q", csp, want)
	}
}

func TestCSPBuilderBuildCheckedRejectsDroppedSources(t *testing.T) {
	builder := NewCSPBuilder().
		DefaultSrc("'self'", "'none'; script-src *").
		ScriptSrc("https://cdn.example.com", "bad\nvalue")

	csp := builder.Build()
	if csp != "" {
		t.Fatalf("Build output = %q, want empty fail-closed output", csp)
	}

	if _, err := builder.BuildChecked(); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("BuildChecked error = %v, want ErrInvalidPolicy", err)
	}
	if err := builder.Validate(); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("Validate error = %v, want ErrInvalidPolicy", err)
	}
}

func TestCSPBuilderBuildCheckedAllowsDirectiveCorrection(t *testing.T) {
	builder := NewCSPBuilder().DefaultSrc("'none'; script-src *")
	if _, err := builder.BuildChecked(); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("BuildChecked initial error = %v, want ErrInvalidPolicy", err)
	}

	builder.DefaultSrc("'self'")
	csp, err := builder.BuildChecked()
	if err != nil {
		t.Fatalf("BuildChecked corrected CSP: %v", err)
	}
	if csp != "default-src 'self'" {
		t.Fatalf("corrected CSP = %q, want default-src 'self'", csp)
	}
}

func TestCSPBuilderAllDirectives(t *testing.T) {
	csp := NewCSPBuilder().
		DefaultSrc("'self'").
		ScriptSrc("'self'").
		StyleSrc("'self'").
		ImgSrc("'self'").
		FontSrc("'self'").
		ConnectSrc("'self'").
		FrameSrc("'self'").
		ObjectSrc("'self'").
		MediaSrc("'self'").
		ChildSrc("'self'").
		FormAction("'self'").
		FrameAncestors("'self'").
		BaseURI("'self'").
		ManifestSrc("'self'").
		WorkerSrc("'self'").
		Build()

	if csp == "" {
		t.Fatal("expected non-empty CSP")
	}

	directives := []string{
		"default-src", "script-src", "style-src", "img-src", "font-src",
		"connect-src", "frame-src", "object-src", "media-src", "child-src",
		"form-action", "frame-ancestors", "base-uri", "manifest-src", "worker-src",
	}

	for _, directive := range directives {
		if !containsDirective(csp, directive+" 'self'") {
			t.Errorf("expected %s 'self'", directive)
		}
	}
}

func TestPolicyWithCSP(t *testing.T) {
	csp := NewCSPBuilder().
		DefaultSrc("'self'").
		ScriptSrc("'self'", "https://cdn.example.com").
		Build()

	policy := Policy{
		ContentSecurityPolicy: csp,
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	w := httptest.NewRecorder()
	policy.Apply(w, req)

	resp := w.Result()
	got := resp.Header.Get("Content-Security-Policy")
	if got != csp {
		t.Errorf("CSP mismatch\ngot:  %q\nwant: %q", got, csp)
	}
}

func containsDirective(csp, directive string) bool {
	for _, part := range strings.Split(csp, ";") {
		part = strings.TrimSpace(part)
		if part == directive || strings.HasPrefix(part, directive+" ") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Additional targeted coverage tests
// ---------------------------------------------------------------------------

// TestApplyCheckedSuccessPath exercises the happy path of ApplyChecked (which
// was previously at 50% because only the error branch was covered).
func TestApplyCheckedSuccessPath(t *testing.T) {
	policy := DefaultPolicy()
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	w := httptest.NewRecorder()

	if err := policy.ApplyChecked(w, req); err != nil {
		t.Fatalf("ApplyChecked unexpected error: %v", err)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// TestCSPBuilderReportTo exercises the ReportTo builder method (0% covered).
func TestCSPBuilderReportTo(t *testing.T) {
	csp := NewCSPBuilder().
		DefaultSrc("'self'").
		ReportTo("csp-endpoint").
		Build()
	if !containsDirective(csp, "report-to csp-endpoint") {
		t.Fatalf("CSP = %q, expected report-to directive", csp)
	}
}

// TestCSPBuilderBlockAllMixedContent exercises the BlockAllMixedContent method
// (0% covered).
func TestCSPBuilderBlockAllMixedContent(t *testing.T) {
	csp := NewCSPBuilder().
		DefaultSrc("'self'").
		BlockAllMixedContent().
		Build()
	if !containsDirective(csp, "block-all-mixed-content") {
		t.Fatalf("CSP = %q, expected block-all-mixed-content directive", csp)
	}
}

// TestEnsureMapsNilMaps exercises the nil-map branch inside ensureMaps by
// creating a CSPBuilder with nil internal maps via zero value and calling a
// builder method that triggers ensureMaps.
func TestEnsureMapsNilMaps(t *testing.T) {
	// A zero-value CSPBuilder has nil directives and invalid maps.
	var b CSPBuilder
	// setDirective calls ensureMaps; must not panic.
	b.DefaultSrc("'self'")
	csp := b.Build()
	if csp == "" {
		t.Fatal("expected non-empty CSP from zero-value builder after setDirective")
	}
}

// TestSetEnumHeaderEmptyValue exercises the empty-value early-return guard in
// setEnumHeader by setting an empty FrameOptions.
func TestSetEnumHeaderEmptyValue(t *testing.T) {
	policy := Policy{
		ContentTypeOptions: "nosniff",
		// FrameOptions is empty → setEnumHeader must skip it entirely.
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	w := httptest.NewRecorder()
	policy.Apply(w, req)

	if got := w.Header().Get("X-Frame-Options"); got != "" {
		t.Fatalf("X-Frame-Options = %q, want empty for empty policy field", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// TestSetHeaderInvalidValue exercises the IsHeaderValue guard inside setHeader
// by supplying a ContentSecurityPolicy containing a control character that
// passes Validate (we bypass validation by testing apply internals via a valid
// policy that had been mutated post-validation). We test the setHeader guard
// directly.
func TestSetHeaderInvalidValueSkipped(t *testing.T) {
	h := make(http.Header)
	setHeader(h, "X-Test", "")
	if h.Get("X-Test") != "" {
		t.Fatal("expected empty value to be skipped")
	}
	setHeader(h, "X-Test", "bad\nvalue")
	if h.Get("X-Test") != "" {
		t.Fatal("expected invalid value to be skipped")
	}
	setHeader(h, "X-Test", "safe")
	if h.Get("X-Test") != "safe" {
		t.Fatal("expected safe value to be set")
	}
}

// TestFormatHSTSNegativeMaxAge exercises the maxAge<0 clamping branch.
func TestFormatHSTSNegativeMaxAge(t *testing.T) {
	opts := HSTSOptions{
		MaxAge:            -1 * time.Second,
		IncludeSubDomains: false,
		Preload:           false,
	}
	got := formatHSTS(opts)
	if got != "max-age=0" {
		t.Fatalf("formatHSTS negative = %q, want max-age=0", got)
	}
}

// TestIsHTTPSRequestNil exercises the nil-request guard in isHTTPSRequest.
func TestIsHTTPSRequestNil(t *testing.T) {
	if isHTTPSRequest(nil) {
		t.Fatal("expected false for nil request")
	}
}

// TestSetFlagDirectiveNilBuilder exercises the nil-receiver guard in
// setFlagDirective (indirect via BlockAllMixedContent on nil builder).
func TestSetFlagDirectiveNilBuilder(t *testing.T) {
	var b *CSPBuilder
	// Must not panic; returns nil.
	result := b.BlockAllMixedContent()
	if result != nil {
		t.Fatal("expected nil from nil-receiver setFlagDirective")
	}
}

func TestCompiledPolicyDetachesAdditionalHeaders(t *testing.T) {
	policy := DefaultPolicy()
	policy.Additional = map[string]string{"X-Custom": "original"}
	compiled := policy.Compile()
	policy.Additional["X-Custom"] = "changed"
	policy.FrameOptions = "DENY"
	for _, tc := range []struct {
		active         Policy
		custom, frames string
	}{{compiled, "original", "SAMEORIGIN"}, {policy.Compile(), "changed", "DENY"}} {
		rec := httptest.NewRecorder()
		if err := tc.active.ApplyChecked(rec, httptest.NewRequest(http.MethodGet, "https://example.test/", nil)); err != nil {
			t.Fatal(err)
		}
		if rec.Header().Get("X-Custom") != tc.custom || rec.Header().Get("X-Frame-Options") != tc.frames {
			t.Fatalf("unexpected compiled policy headers: %v", rec.Header())
		}
	}
}

func TestAdditionalHeaderNamesMustBeUniqueAfterCanonicalization(t *testing.T) {
	p := DefaultPolicy()
	p.Additional = map[string]string{"X-Fixture": "first", "x-fixture": "second"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	if err := p.Compile().ApplyChecked(w, req); !errors.Is(err, ErrInvalidPolicy) || len(w.Header()) != 0 {
		t.Fatalf("error=%v headers=%v", err, w.Header())
	}
	delete(p.Additional, "X-Fixture")
	if err := p.Compile().ApplyChecked(w, req); err != nil || w.Header().Get("X-Fixture") != "second" {
		t.Fatalf("error=%v headers=%v", err, w.Header())
	}
}

func BenchmarkCompiledHeaderValidation(b *testing.B) {
	for _, count := range []int{0, 32} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			policy := DefaultPolicy()
			policy.Additional = make(map[string]string, count)
			for i := range count {
				policy.Additional[fmt.Sprintf("X-Fixture-%d", i)] = "value"
			}
			compiled := policy.Compile()
			req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
			w := httptest.NewRecorder()
			b.ReportAllocs()
			for b.Loop() {
				if err := compiled.ApplyChecked(w, req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestMultipleInvalidPolicyFieldsPreserveClassificationAndHeaders(t *testing.T) {
	policy := DefaultPolicy()
	policy.FrameOptions = "fixture-option"
	policy.StrictTransportSecurity = &HSTSOptions{MaxAge: -1}
	w := httptest.NewRecorder()
	w.Header().Set("X-Application", "fixture")
	err := policy.ApplyChecked(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if !errors.Is(err, ErrInvalidPolicy) || !strings.Contains(err.Error(), "X-Frame-Options") ||
		!strings.Contains(err.Error(), "Strict-Transport-Security") || len(w.Header()) != 1 ||
		w.Header().Get("X-Application") != "fixture" {
		t.Fatalf("error=%v headers=%v", err, w.Header())
	}
}

func TestCompiledPolicyKeepsPrepopulatedWriterOnValidationFailure(t *testing.T) {
	policy := Policy{Additional: map[string]string{"X-Fixture": "compiled"}}
	compiled := policy.Compile()
	policy.Additional["X-Fixture"] = "source changed"
	policy.FrameOptions = "unknown"
	req := httptest.NewRequest("GET", "https://example.test/", nil)
	dst := httptest.NewRecorder()
	dst.Header().Set("X-Fixture", "existing")
	dst.Header().Set("X-Preserved", "value")
	if err := policy.ApplyChecked(dst, req); !errors.Is(err, ErrInvalidPolicy) || dst.Header().Get("X-Fixture") != "existing" {
		t.Fatalf("source err=%v header=%v", err, dst.Header())
	}
	if err := compiled.ApplyChecked(dst, req); err != nil || dst.Header().Get("X-Fixture") != "compiled" || dst.Header().Get("X-Preserved") != "value" {
		t.Fatalf("compile err=%v header=%v", err, dst.Header())
	}
}

func TestCSPBuildKeepsDirectiveOrderAndSourceSnapshot(t *testing.T) {
	sources := []string{"'self'", "https://example.test"}
	b := NewCSPBuilder().ImgSrc("data:").ScriptSrc(sources...).DefaultSrc("'none'")
	first, err := b.BuildChecked()
	if err != nil || first != "default-src 'none'; script-src 'self' https://example.test; img-src data:" {
		t.Fatalf("first=%q err=%v", first, err)
	}
	sources[1] = "https://different.test"
	if next := b.Build(); next != first {
		t.Fatalf("source changed output=%q", next)
	}
	b.ScriptSrc("'none'")
	if next := b.Build(); next != "default-src 'none'; script-src 'none'; img-src data:" || !strings.Contains(first, "https://example.test") {
		t.Fatalf("new=%q first=%q", next, first)
	}
}

func TestCSPSandboxFlagAndValueReplacement(t *testing.T) {
	builder := NewCSPBuilder().DefaultSrc("'self'")
	for _, step := range []struct {
		values []string
		value  string
	}{{nil, "sandbox"}, {[]string{"allow-scripts"}, "sandbox allow-scripts"}, {nil, "sandbox"}} {
		got, err := builder.Sandbox(step.values...).BuildChecked()
		if err != nil || got != "default-src 'self'; "+step.value {
			t.Fatalf("policy=%q error=%v", got, err)
		}
	}
}

func TestCompiledPolicyKeepsHSTSRequestLocal(t *testing.T) {
	policy := Policy{StrictTransportSecurity: &HSTSOptions{MaxAge: 60 * time.Second}}.Compile()
	for _, fixture := range []struct{ url, header string }{
		{"https://fixture.test/", "max-age=60"},
		{"http://fixture.test/", ""},
		{"https://fixture.test/", "max-age=60"},
	} {
		rec := httptest.NewRecorder()
		if err := policy.ApplyChecked(rec, httptest.NewRequest(http.MethodGet, fixture.url, nil)); err != nil {
			t.Fatal(err)
		}
		if got := rec.Header().Get("Strict-Transport-Security"); got != fixture.header {
			t.Fatalf("HSTS=%q want=%q", got, fixture.header)
		}
	}
}

func TestCompiledHSTSRequiresPositiveAgeAndTLS(t *testing.T) {
	for _, tc := range []struct {
		tls  bool
		age  time.Duration
		want string
	}{
		{false, 0, ""}, {true, 0, ""},
		{false, time.Minute, ""}, {true, time.Minute, "max-age=60"},
	} {
		p := Policy{StrictTransportSecurity: &HSTSOptions{MaxAge: tc.age}}.Compile()
		req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
		if tc.tls {
			req.TLS = &tls.ConnectionState{}
		}
		w := httptest.NewRecorder()
		if err := p.ApplyChecked(w, req); err != nil || w.Header().Get("Strict-Transport-Security") != tc.want {
			t.Fatalf("TLS=%t age=%v headers=%v err=%v", tc.tls, tc.age, w.Header(), err)
		}
	}
}
