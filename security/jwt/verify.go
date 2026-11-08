package jwt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// VerifyToken verifies token signature and semantic checks.
func (m *JWTManager) VerifyToken(ctx context.Context, token string, expectedType TokenType) (*TokenClaims, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}

	claims, err := m.parseAndVerify(token)
	if err != nil {
		return nil, err
	}
	if err := contextErr(ctx); err != nil {
		return nil, err
	}

	now := m.currentTime().Unix()
	skew := int64(m.config.ClockSkew.Seconds())

	if claims.IssuedAt <= 0 || claims.NotBefore <= 0 || claims.ExpiresAt <= 0 {
		return nil, ErrInvalidToken
	}
	if now < claims.IssuedAt-skew {
		return nil, ErrInvalidToken
	}
	if now-skew >= claims.ExpiresAt {
		return nil, ErrTokenExpired
	}
	if now < claims.NotBefore-skew {
		return nil, ErrTokenNotYetValid
	}

	if expectedType != "" && claims.TokenType != expectedType {
		return nil, ErrInvalidToken
	}
	if claims.Identity.Subject == "" {
		return nil, ErrMissingSubject
	}

	return claims, nil
}

func (m *JWTManager) parseAndVerify(token string) (*TokenClaims, error) {
	parts, ok := splitCompactJWT(token)
	if !ok {
		return nil, ErrInvalidToken
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var header map[string]any
	if err = json.Unmarshal(headerJSON, &header); err != nil {
		return nil, ErrInvalidToken
	}

	kid, _ := header["kid"].(string)
	algStr, _ := header["alg"].(string)
	typ, _ := header["typ"].(string)
	if kid == "" || algStr == "" || typ != "JWT" {
		return nil, ErrInvalidToken
	}

	m.mu.RLock()
	key, ok := m.keyCache[kid]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownKey
	}
	if string(key.Algorithm) != algStr {
		return nil, ErrInvalidToken
	}

	if err := verifySignature(key, parts[0], parts[1], parts[2]); err != nil {
		return nil, err
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var claims TokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrInvalidToken
	}
	if claims.KeyID != kid {
		return nil, ErrInvalidToken
	}

	if m.config.Issuer != "" && claims.Issuer != m.config.Issuer {
		return nil, ErrInvalidIssuer
	}
	if m.config.Audience != "" && claims.Audience != m.config.Audience {
		return nil, ErrInvalidAudience
	}

	return &claims, nil
}

func splitCompactJWT(token string) ([3]string, bool) {
	var parts [3]string
	if token == "" || len(token) > maxJWTTokenLength {
		return parts, false
	}

	header, rest, ok := strings.Cut(token, ".")
	if !ok {
		return parts, false
	}
	payload, signature, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(signature, ".") {
		return parts, false
	}

	parts = [3]string{header, payload, signature}
	for _, part := range parts {
		if part == "" || len(part) > maxJWTSegmentLength {
			return [3]string{}, false
		}
	}
	return parts, true
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
