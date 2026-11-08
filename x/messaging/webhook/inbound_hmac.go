package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/internal/httputil"
)

// SignatureEncoding defines how the signature is encoded in headers.
type SignatureEncoding string

const (
	EncodingHex          SignatureEncoding = "hex"
	EncodingBase64       SignatureEncoding = "base64"
	EncodingBase64Raw    SignatureEncoding = "base64raw"
	EncodingBase64URL    SignatureEncoding = "base64url"
	EncodingBase64URLRaw SignatureEncoding = "base64urlraw"
)

// HashAlgorithm defines the HMAC hash algorithm.
type HashAlgorithm string

const (
	HashSHA256 HashAlgorithm = "sha256"
	HashSHA512 HashAlgorithm = "sha512"
)

// NonceStore provides nonce replay protection for webhook deliveries.
// Without a NonceStore, any attacker who captures a valid signed request can
// replay it within the signature's validity window. Configure a NonceStore
// (e.g. NewMemoryNonceStore) in production to prevent replay attacks.
type NonceStore interface {
	Seen(ctx context.Context, nonce string) (bool, error)
}

// MemoryNonceStore is an in-memory nonce store backed by Deduper.
type MemoryNonceStore struct {
	deduper *Deduper
}

// NewMemoryNonceStore creates a nonce store with the given TTL.
func NewMemoryNonceStore(ttl time.Duration) *MemoryNonceStore {
	return &MemoryNonceStore{deduper: NewDeduper(ttl)}
}

func (s *MemoryNonceStore) Seen(ctx context.Context, nonce string) (bool, error) {
	if s == nil || s.deduper == nil {
		return false, nil
	}
	if strings.TrimSpace(nonce) == "" {
		return false, nil
	}
	return s.deduper.SeenBefore(nonce), nil
}

// HMACReplayConfig configures replay protection.
type HMACReplayConfig struct {
	TimestampHeader string
	NonceHeader     string
	Tolerance       time.Duration
	Now             func() time.Time
	// NonceStore prevents replay attacks by tracking seen nonce values.
	// Required for replay protection: without it, any intercepted delivery
	// can be replayed within the Tolerance window.
	NonceStore NonceStore
}

// HMACConfig configures generic HMAC verification.
type HMACConfig struct {
	Secret       []byte
	Header       string
	Prefix       string
	MaxBody      int64
	Algorithm    HashAlgorithm
	Encoding     SignatureEncoding
	Replay       HMACReplayConfig
	IPAllowlist  *IPAllowlist
	IPResolver   func(*http.Request) string
	SignedFormat SignedPayloadFormat
}

// SignedPayloadFormat controls how the signing payload is constructed.
type SignedPayloadFormat string

const (
	// PayloadRaw signs just the raw body.
	PayloadRaw SignedPayloadFormat = "raw"
	// PayloadTimestampBody signs "timestamp.body" when timestamp is provided.
	PayloadTimestampBody SignedPayloadFormat = "timestamp_body"
	// PayloadTimestampNonceBody signs "timestamp.nonce.body" when both are provided.
	PayloadTimestampNonceBody SignedPayloadFormat = "timestamp_nonce_body"
	// PayloadNonceBody signs "nonce.body" when nonce is provided.
	PayloadNonceBody SignedPayloadFormat = "nonce_body"
)

// VerifyResult contains verification metadata.
type VerifyResult struct {
	Body      []byte
	Signature string
	Timestamp time.Time
	Nonce     string
	IP        string
}

// VerifyHMAC verifies a generic HMAC-signed webhook request.
func VerifyHMAC(r *http.Request, cfg HMACConfig) (VerifyResult, error) {
	var result VerifyResult
	if r == nil {
		return result, verifyError(CodeConfigError, "request is nil", nil)
	}
	if len(cfg.Secret) == 0 {
		return result, verifyError(CodeConfigError, "missing secret", nil)
	}
	if strings.TrimSpace(cfg.Header) == "" {
		return result, verifyError(CodeConfigError, "missing signature header", nil)
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = 1 << 20
	}
	if cfg.Algorithm == "" {
		cfg.Algorithm = HashSHA256
	}
	if cfg.Encoding == "" {
		cfg.Encoding = EncodingHex
	}
	if cfg.Replay.Tolerance <= 0 && (cfg.Replay.TimestampHeader != "" || cfg.Replay.NonceHeader != "") {
		cfg.Replay.Tolerance = 5 * time.Minute
	}
	if cfg.Replay.Now == nil {
		cfg.Replay.Now = time.Now
	}
	if cfg.SignedFormat == "" {
		cfg.SignedFormat = PayloadTimestampNonceBody
	}

	if cfg.IPAllowlist != nil {
		resolver := cfg.IPResolver
		if resolver == nil {
			resolver = httputil.ClientIP
		}
		ip := strings.TrimSpace(resolver(r))
		result.IP = ip
		if ip == "" || !cfg.IPAllowlist.Allow(ip) {
			return result, verifyError(CodeIPDenied, "ip not allowed", nil)
		}
	}

	headerVal := strings.TrimSpace(r.Header.Get(cfg.Header))
	if headerVal == "" {
		return result, verifyError(CodeMissingSignature, "missing signature header", nil)
	}

	if cfg.Prefix != "" {
		if !strings.HasPrefix(headerVal, cfg.Prefix) {
			return result, verifyError(CodeInvalidSignature, "invalid signature prefix", nil)
		}
		headerVal = strings.TrimPrefix(headerVal, cfg.Prefix)
		headerVal = strings.TrimSpace(headerVal)
	}
	if headerVal == "" {
		return result, verifyError(CodeInvalidSignature, "empty signature", nil)
	}
	result.Signature = headerVal

	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, cfg.MaxBody))
	if err != nil {
		return result, verifyError(CodeBodyRead, "read body failed", err)
	}
	result.Body = body

	var nonce string
	var timestamp time.Time
	if cfg.Replay.TimestampHeader != "" {
		tsRaw := strings.TrimSpace(r.Header.Get(cfg.Replay.TimestampHeader))
		if tsRaw == "" {
			return result, verifyError(CodeMissingTimestamp, "missing timestamp", nil)
		}
		parsed, err := strconv.ParseInt(tsRaw, 10, 64)
		if err != nil {
			return result, verifyError(CodeInvalidTimestamp, "invalid timestamp", err)
		}
		timestamp = time.Unix(parsed, 0).UTC()
		now := cfg.Replay.Now().UTC()
		earliest := now.Add(-cfg.Replay.Tolerance)
		latest := now.Add(cfg.Replay.Tolerance)
		if timestamp.Before(earliest) {
			return result, verifyError(CodeTimestampExpired, "timestamp expired", nil)
		}
		if timestamp.After(latest) {
			return result, verifyError(CodeInvalidTimestamp, "timestamp too far in future", nil)
		}
		result.Timestamp = timestamp
	}

	if cfg.Replay.NonceHeader != "" {
		nonce = strings.TrimSpace(r.Header.Get(cfg.Replay.NonceHeader))
		if nonce == "" {
			return result, verifyError(CodeMissingNonce, "missing nonce", nil)
		}
		result.Nonce = nonce
		if cfg.Replay.NonceStore != nil {
			seen, err := cfg.Replay.NonceStore.Seen(r.Context(), nonce)
			if err != nil {
				return result, verifyError(CodeInternalError, "nonce store error", err)
			}
			if seen {
				return result, verifyError(CodeReplayDetected, "replay detected", nil)
			}
		}
	}

	signedPayload := buildSignedPayload(cfg.SignedFormat, body, timestamp, nonce, cfg.Replay.TimestampHeader != "", cfg.Replay.NonceHeader != "")
	expected, err := computeHMAC(cfg.Algorithm, cfg.Secret, signedPayload)
	if err != nil {
		return result, verifyError(CodeConfigError, "invalid hash algorithm", err)
	}

	candidate, err := decodeSignature(cfg.Encoding, headerVal)
	if err != nil {
		return result, verifyError(CodeInvalidEncoding, "invalid signature encoding", err)
	}
	if !hmac.Equal(expected, candidate) {
		return result, verifyError(CodeInvalidSignature, "signature mismatch", nil)
	}

	return result, nil
}

func buildSignedPayload(format SignedPayloadFormat, body []byte, timestamp time.Time, nonce string, hasTimestamp bool, hasNonce bool) []byte {
	switch format {
	case PayloadRaw:
		return body
	case PayloadTimestampBody:
		if hasTimestamp {
			return []byte(strconv.FormatInt(timestamp.Unix(), 10) + "." + string(body))
		}
	case PayloadNonceBody:
		if hasNonce {
			return []byte(nonce + "." + string(body))
		}
	case PayloadTimestampNonceBody:
		if hasTimestamp && hasNonce {
			return []byte(strconv.FormatInt(timestamp.Unix(), 10) + "." + nonce + "." + string(body))
		}
		if hasTimestamp {
			return []byte(strconv.FormatInt(timestamp.Unix(), 10) + "." + string(body))
		}
		if hasNonce {
			return []byte(nonce + "." + string(body))
		}
	default:
	}
	return body
}

func computeHMAC(alg HashAlgorithm, secret, payload []byte) ([]byte, error) {
	var h func() hash.Hash
	switch alg {
	case HashSHA256:
		h = sha256.New
	case HashSHA512:
		h = sha512.New
	default:
		return nil, fmt.Errorf("unsupported hash %s", alg)
	}

	mac := hmac.New(h, secret)
	_, _ = mac.Write(payload)
	return mac.Sum(nil), nil
}

func decodeSignature(enc SignatureEncoding, sig string) ([]byte, error) {
	switch enc {
	case EncodingHex:
		return hex.DecodeString(sig)
	case EncodingBase64:
		return base64.StdEncoding.DecodeString(sig)
	case EncodingBase64Raw:
		return base64.RawStdEncoding.DecodeString(sig)
	case EncodingBase64URL:
		return base64.URLEncoding.DecodeString(sig)
	case EncodingBase64URLRaw:
		return base64.RawURLEncoding.DecodeString(sig)
	default:
		return nil, errors.New("unsupported encoding")
	}
}
