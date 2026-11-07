package file

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestNewS3Signer(t *testing.T) {
	signer := NewS3Signer("access-key", "secret-key", "us-east-1")

	if signer == nil {
		t.Fatal("Signer is nil")
	}
	if signer.accessKey != "access-key" {
		t.Errorf("accessKey = %q, want %q", signer.accessKey, "access-key")
	}
	if signer.secretKey != "secret-key" {
		t.Errorf("secretKey = %q, want %q", signer.secretKey, "secret-key")
	}
	if signer.region != "us-east-1" {
		t.Errorf("region = %q, want %q", signer.region, "us-east-1")
	}
	if signer.service != "s3" {
		t.Errorf("service = %q, want %q", signer.service, "s3")
	}
}

func TestS3Signer_SignRequest(t *testing.T) {
	signer := NewS3Signer("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1")

	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := signer.SignRequest(req, emptyStringSHA256()); err != nil {
		t.Fatalf("SignRequest failed: %v", err)
	}

	if req.Header.Get("x-amz-date") == "" {
		t.Error("x-amz-date header not set")
	}
	if req.Header.Get("x-amz-content-sha256") == "" {
		t.Error("x-amz-content-sha256 header not set")
	}
	if req.Header.Get("Authorization") == "" {
		t.Error("Authorization header not set")
	}

	auth := req.Header.Get("Authorization")
	for _, want := range []string{"AWS4-HMAC-SHA256", "Credential=", "SignedHeaders=", "Signature="} {
		if !containsString(auth, want) {
			t.Errorf("Authorization should contain %q", want)
		}
	}
}

func TestS3Signer_SignRequest_WithPayloadHash(t *testing.T) {
	signer := NewS3Signer("access-key", "secret-key", "us-east-1")

	req, err := http.NewRequest(http.MethodPut, "https://bucket.s3.amazonaws.com/key", nil)
	if err != nil {
		t.Fatal(err)
	}

	customHash := "abcd1234"
	if err := signer.SignRequest(req, customHash); err != nil {
		t.Fatal(err)
	}

	if req.Header.Get("x-amz-content-sha256") != customHash {
		t.Errorf("x-amz-content-sha256 = %q, want %q", req.Header.Get("x-amz-content-sha256"), customHash)
	}
}

func TestS3Signer_PresignRequest(t *testing.T) {
	signer := NewS3Signer("AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1")

	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}

	presignedURL, err := signer.PresignRequest(req, 15*time.Minute)
	if err != nil {
		t.Fatalf("PresignRequest failed: %v", err)
	}

	u, err := url.Parse(presignedURL)
	if err != nil {
		t.Fatalf("Failed to parse presigned URL: %v", err)
	}

	query := u.Query()
	if query.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
		t.Errorf("X-Amz-Algorithm = %q", query.Get("X-Amz-Algorithm"))
	}
	if query.Get("X-Amz-Expires") != "900" {
		t.Errorf("X-Amz-Expires = %q, want 900", query.Get("X-Amz-Expires"))
	}
	if query.Get("X-Amz-Signature") == "" {
		t.Error("X-Amz-Signature not set")
	}
	if req.Host != "examplebucket.s3.amazonaws.com" {
		t.Fatalf("request Host = %q, want URL host", req.Host)
	}
}

func TestS3Signer_PresignRequest_ExpiryValidation(t *testing.T) {
	signer := NewS3Signer("access-key", "secret-key", "us-east-1")

	tests := []struct {
		name       string
		expiry     time.Duration
		wantExpiry string
	}{
		{"valid", time.Hour, "3600"},
		{"zero defaults to 15min", 0, "900"},
		{"negative defaults to 15min", -time.Hour, "900"},
		{"too long defaults to 15min", 8 * 24 * time.Hour, "900"},
		{"max 7 days", 7 * 24 * time.Hour, "604800"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "https://bucket.s3.amazonaws.com/key", nil)
			presignedURL, err := signer.PresignRequest(req, tt.expiry)
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(presignedURL)
			if got := u.Query().Get("X-Amz-Expires"); got != tt.wantExpiry {
				t.Errorf("X-Amz-Expires = %q, want %q", got, tt.wantExpiry)
			}
		})
	}
}

func TestS3Signer_BuildCanonicalQueryString(t *testing.T) {
	signer := NewS3Signer("access-key", "secret-key", "us-east-1")

	tests := []struct {
		name   string
		values url.Values
		want   string
	}{
		{"empty", url.Values{}, ""},
		{"single", url.Values{"key": []string{"value"}}, "key=value"},
		{"sorted", url.Values{"zebra": []string{"z"}, "alpha": []string{"a"}, "beta": []string{"b"}}, "alpha=a&beta=b&zebra=z"},
		{"aws space encoding", url.Values{"key name": []string{"value with space"}}, "key%20name=value%20with%20space"},
		{"sorted values", url.Values{"k": []string{"z", "a b"}}, "k=a%20b&k=z"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := signer.buildCanonicalQueryString(tt.values)
			if got != tt.want {
				t.Errorf("buildCanonicalQueryString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEmptyStringSHA256(t *testing.T) {
	got := emptyStringSHA256()
	hash := sha256.Sum256([]byte{})
	want := hex.EncodeToString(hash[:])
	if got != want {
		t.Errorf("emptyStringSHA256() = %q, want %q", got, want)
	}
	if len(got) != 64 {
		t.Errorf("Hash length = %d, want 64", len(got))
	}
}

func TestHmacSHA256(t *testing.T) {
	key := []byte("secret")
	data := []byte("message")
	result := hmacSHA256(key, data)

	if len(result) != 32 {
		t.Errorf("HMAC length = %d, want 32", len(result))
	}

	result2 := hmacSHA256(key, data)
	if !bytesEqual(result, result2) {
		t.Error("HMAC should be deterministic")
	}

	result3 := hmacSHA256([]byte("different"), data)
	if bytesEqual(result, result3) {
		t.Error("Different keys should produce different HMAC")
	}
}

func TestS3Signer_CalculateSignature(t *testing.T) {
	signer := NewS3Signer("access-key", "secret-key", "us-east-1")
	sig := signer.calculateSignature("20260205", "test-string")

	if len(sig) != 64 {
		t.Errorf("Signature length = %d, want 64", len(sig))
	}
	sig2 := signer.calculateSignature("20260205", "test-string")
	if sig != sig2 {
		t.Error("Signature should be deterministic")
	}
	sig3 := signer.calculateSignature("20260206", "test-string")
	if sig == sig3 {
		t.Error("Different dates should produce different signatures")
	}
}

// Benchmarks

func BenchmarkS3Signer_SignRequest(b *testing.B) {
	signer := NewS3Signer("access-key", "secret-key", "us-east-1")
	req, _ := http.NewRequest(http.MethodGet, "https://bucket.s3.amazonaws.com/key", nil)
	payloadHash := emptyStringSHA256()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = signer.SignRequest(req, payloadHash)
	}
}

// Helper functions

func containsString(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
