// Package input provides security-focused input validation and basic
// defense-in-depth sanitization utilities.
//
// This package implements conservative validation functions for common input
// types including:
//   - Email addresses with DNS-style domain label checks
//   - Safe HTTP and HTTPS URLs plus safe absolute relative URL paths
//   - Phone numbers with E.164-compatible digit bounds
//   - HTTP tokens (RFC 7230)
//
// The sanitizer helpers are not substitutes for context-aware HTML sanitizers,
// parameterized SQL queries, or output encoding.
//
// Example usage:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	// Validate email
//	if !input.ValidateEmail("user@example.com") {
//		// Invalid email
//	}
//
//	// Validate URL
//	if !input.ValidateURL("https://example.com") {
//		// Invalid URL
//	}
//
//	// Validate phone number (E.164)
//	if !input.ValidatePhone("+1234567890") {
//		// Invalid phone number
//	}
//
//	// Check if string contains dangerous characters
//	if input.ContainsDangerousChars(userInput) {
//		// Contains dangerous characters
//	}
package input

import (
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	htmlScriptTagRe     = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	htmlEventHandlerRe  = regexp.MustCompile(`(?i)\s+on[a-z0-9_-]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)`)
	htmlJavaScriptURLRe = regexp.MustCompile(`(?i)javascript:`)
	htmlDataURLRe       = regexp.MustCompile(`(?i)data:`)
	sqlLineCommentRe    = regexp.MustCompile(`--.*`)
	sqlBlockCommentRe   = regexp.MustCompile(`(?s)/\*.*?\*/`)
	sqlKeywordRe        = regexp.MustCompile(`(?i)\b(?:UNION|SELECT|INSERT|UPDATE|DELETE|DROP|CREATE|ALTER|EXECUTE|EXEC)\b`)
	whitespaceRe        = regexp.MustCompile(`\s+`)
)

// IsToken reports whether value is a valid HTTP token (RFC 7230).
//
// HTTP tokens are used in header names, media types, and other HTTP constructs.
// A token consists of ASCII letters, digits, and certain special characters.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	if input.IsToken("Content-Type") {
//		// Valid HTTP token
//	}
//
// Valid tokens include:
//   - Letters (a-z, A-Z)
//   - Digits (0-9)
//   - Special characters: ! # $ % & ' * + - . ^ _ ` | ~
func IsToken(value string) bool {
	if value == "" {
		return false
	}

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if !isTchar(ch) {
			return false
		}
	}

	return true
}

// IsHeaderName reports whether value is safe for use as an HTTP header name.
//
// Header names must be valid HTTP tokens according to RFC 7230.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	if input.IsHeaderName("X-Custom-Header") {
//		// Valid header name
//	}
func IsHeaderName(value string) bool {
	return IsToken(value)
}

// IsHeaderValue reports whether value is safe for use as an HTTP header value.
// It rejects control characters that can lead to response splitting.
//
// Header values must be valid UTF-8 and must not contain ASCII control
// characters except horizontal tab.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	if input.IsHeaderValue("some value") {
//		// Safe to use as header value
//	}
func IsHeaderValue(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}

	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch == '\t' {
			continue
		}
		if ch < 0x20 || ch == 0x7f {
			return false
		}
	}

	return true
}

func isTchar(ch byte) bool {
	if ch >= '0' && ch <= '9' {
		return true
	}
	if ch >= 'a' && ch <= 'z' {
		return true
	}
	if ch >= 'A' && ch <= 'Z' {
		return true
	}

	switch ch {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

// ValidateEmail performs security-focused email validation.
//
// Returns true if the email appears valid. This is a basic check for security
// purposes, not a comprehensive email validation.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	if !input.ValidateEmail(userEmail) {
//		// Reject invalid email
//	}
func ValidateEmail(email string) bool {
	email = strings.TrimSpace(email)
	if email == "" {
		return false
	}
	if len(email) > 254 || strings.ContainsRune(email, 0) || !utf8.ValidString(email) {
		return false
	}

	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}

	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" {
		return false
	}
	if len(local) > 64 || strings.Contains(local, "..") || strings.Contains(domain, "..") {
		return false
	}
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") {
		return false
	}

	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !isEmailDomainLabel(label) {
			return false
		}
	}
	return true
}

func isEmailDomainLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return false
	}
	for i := 0; i < len(label); i++ {
		ch := label[i]
		if ch >= 'a' && ch <= 'z' {
			continue
		}
		if ch >= 'A' && ch <= 'Z' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '-' {
			continue
		}
		return false
	}
	return true
}

// ValidateURL checks URL shape, allowed schemes, and unsafe relative forms.
//
// Returns true if the URL is syntactically valid for common redirect/link use.
// This function rejects javascript:, data:, file:, vbscript:, embedded
// credentials, malformed URLs, and unsafe relative paths. It does not make a URL
// safe for server-side fetching because hostnames can resolve to private
// addresses. Use ValidatePublicURL for SSRF-sensitive fetch targets.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	if !input.ValidateURL(userURL) {
//		// Reject invalid or unsafe URL
//	}
func ValidateURL(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	if len(rawURL) > 2048 || strings.ContainsRune(rawURL, 0) || !utf8.ValidString(rawURL) {
		return false
	}

	lowerURL := strings.ToLower(rawURL)
	for _, blocked := range []string{"javascript:", "data:", "file:", "vbscript:"} {
		if strings.HasPrefix(lowerURL, blocked) {
			return false
		}
	}

	if strings.HasPrefix(rawURL, "/") {
		return isSafeRelativeURLPath(rawURL)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.User != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "" {
		return false
	}
	if !hasStrictDecimalURLPort(parsed.Host) {
		return false
	}
	return true
}

// ValidatePublicURL checks whether rawURL is an absolute HTTP(S) URL whose
// literal host is suitable for server-side fetch allow-lists.
//
// It rejects relative paths, localhost names, loopback, private, link-local,
// multicast, unspecified, and metadata-service IP targets. It does not perform
// DNS resolution; callers that resolve hostnames must still enforce the same
// checks on every resolved address.
func ValidatePublicURL(rawURL string) bool {
	if !ValidateURL(rawURL) {
		return false
	}

	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if strings.Contains(host, "%") {
		return false
	}
	if host == "metadata.google.internal" {
		return false
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return true
	}
	return isPublicIP(ip)
}

func isPublicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() &&
		!ip.IsPrivate() &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast() &&
		!ip.IsMulticast() &&
		!ip.IsUnspecified()
}

func isSafeRelativeURLPath(rawURL string) bool {
	if strings.HasPrefix(rawURL, "//") {
		return false
	}
	if strings.Contains(rawURL, `\`) {
		return false
	}
	for i := 0; i < len(rawURL); i++ {
		if rawURL[i] <= 0x20 || rawURL[i] == 0x7f {
			return false
		}
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "" && parsed.Host == "" && parsed.User == nil && strings.HasPrefix(parsed.Path, "/")
}

func hasStrictDecimalURLPort(host string) bool {
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end == -1 {
			return false
		}
		rest := host[end+1:]
		if rest == "" {
			return true
		}
		if !strings.HasPrefix(rest, ":") {
			return false
		}
		return isStrictDecimalPort(rest[1:])
	}

	if !strings.Contains(host, ":") {
		return true
	}

	colon := strings.LastIndexByte(host, ':')
	if strings.Contains(host[:colon], ":") {
		return false
	}
	return isStrictDecimalPort(host[colon+1:])
}

func isStrictDecimalPort(port string) bool {
	if port == "" || len(port) > 5 {
		return false
	}

	n := 0
	for i := 0; i < len(port); i++ {
		ch := port[i]
		if ch < '0' || ch > '9' {
			return false
		}
		n = n*10 + int(ch-'0')
	}
	return n <= 65535
}

// ValidatePhone performs basic phone number validation.
//
// Returns true if the phone number appears valid. This is a basic check that
// accepts E.164 format and common phone number patterns.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	if !input.ValidatePhone(userPhone) {
//		// Reject invalid phone
//	}
func ValidatePhone(phone string) bool {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return false
	}
	if !utf8.ValidString(phone) {
		return false
	}

	digits := make([]rune, 0, len(phone))
	for i, r := range phone {
		switch {
		case r >= '0' && r <= '9':
			digits = append(digits, r)
		case r == '+':
			if i != 0 {
				return false
			}
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')':
		default:
			return false
		}
	}

	return len(digits) >= 7 && len(digits) <= 15
}

// BestEffortSanitizeHTML applies a lossy, best-effort HTML cleanup pass.
//
// This helper removes a small set of common script and inline-handler patterns
// for defense-in-depth only. It is not a complete HTML parser or sanitizer and
// must not be used as the sole control for rendering untrusted HTML.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	cleaned := input.BestEffortSanitizeHTML(userInput)
func BestEffortSanitizeHTML(s string) string {
	// Remove script tags and content
	s = htmlScriptTagRe.ReplaceAllString(s, "")

	// Remove event handlers (onclick, onload, etc.)
	s = htmlEventHandlerRe.ReplaceAllString(s, "")

	// Remove javascript: URLs
	s = htmlJavaScriptURLRe.ReplaceAllString(s, "")

	// Remove data: URLs (can be used for XSS)
	s = htmlDataURLRe.ReplaceAllString(s, "")

	return s
}

// BestEffortSanitizeSQL applies a lossy, best-effort SQL text cleanup pass.
//
// WARNING: This is NOT a substitute for parameterized queries. Always use
// parameterized queries for database operations. This helper removes a small
// set of common comment, separator, and keyword patterns for defense-in-depth
// scenarios only.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	// Still use parameterized queries!
//	cleaned := input.BestEffortSanitizeSQL(userInput)
func BestEffortSanitizeSQL(s string) string {
	// Remove SQL comments
	s = sqlLineCommentRe.ReplaceAllString(s, "")
	s = sqlBlockCommentRe.ReplaceAllString(s, "")

	s = strings.ReplaceAll(s, ";", "")
	s = sqlKeywordRe.ReplaceAllString(s, "")

	return s
}

// StripControlChars removes ASCII control characters except newline and tab.
//
// This function removes characters that could cause display issues or
// be used for terminal injection attacks.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	clean := input.StripControlChars(userInput)
func StripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		// Keep newline and tab
		if r == '\n' || r == '\t' {
			return r
		}
		// Remove other control characters
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// ContainsDangerousChars checks for characters commonly used in injection attacks.
//
// Returns true if the string contains potentially dangerous characters.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	if input.ContainsDangerousChars(userInput) {
//		// Handle potentially dangerous input
//	}
func ContainsDangerousChars(s string) bool {
	dangerous := []rune{0, '\r', '<', '>', '"', '\'', '`', '\\', '{', '}'}
	for _, r := range s {
		for _, d := range dangerous {
			if r == d {
				return true
			}
		}
	}
	return false
}

// TrimWhitespace removes leading and trailing whitespace and normalizes internal whitespace.
//
// This function collapses multiple consecutive whitespace characters into a
// single space and removes leading/trailing whitespace.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/input"
//
//	normalized := input.TrimWhitespace("  hello    world  ")
//	// Returns: "hello world"
func TrimWhitespace(s string) string {
	s = strings.TrimSpace(s)

	// Replace multiple spaces with single space
	s = whitespaceRe.ReplaceAllString(s, " ")

	return s
}
