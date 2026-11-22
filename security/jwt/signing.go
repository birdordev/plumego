package jwt

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
)

func verifySignature(key JWTSigningKey, signed, sigPart string) error {
	var scratch [ed25519.SignatureSize]byte
	signature, err := base64.RawURLEncoding.AppendDecode(scratch[:0], []byte(sigPart))
	if err != nil {
		return ErrInvalidToken
	}
	switch key.Algorithm {
	case AlgorithmHS256:
		mac := hmac.New(sha256.New, key.Secret)
		mac.Write([]byte(signed))
		if !hmac.Equal(mac.Sum(nil), signature) {
			return ErrInvalidToken
		}
	case AlgorithmEdDSA:
		if !ed25519.Verify(key.Public, []byte(signed), signature) {
			return ErrInvalidToken
		}
	default:
		return ErrInvalidToken
	}
	return nil
}

func signJWT(key JWTSigningKey, claims TokenClaims) (string, error) {
	header := map[string]any{
		"alg": key.Algorithm,
		"typ": "JWT",
		"kid": key.ID,
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	headerPart := base64.RawURLEncoding.EncodeToString(headerJSON)
	payloadPart := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := headerPart + "." + payloadPart

	var signature []byte
	switch key.Algorithm {
	case AlgorithmHS256:
		mac := hmac.New(sha256.New, key.Secret)
		mac.Write([]byte(signingInput))
		signature = mac.Sum(nil)
	case AlgorithmEdDSA:
		signature = ed25519.Sign(ed25519.PrivateKey(key.Secret), []byte(signingInput))
	default:
		return "", ErrInvalidToken
	}

	sigPart := base64.RawURLEncoding.EncodeToString(signature)
	token := signingInput + "." + sigPart
	if _, ok := splitCompactJWT(token); !ok {
		return "", ErrInvalidToken
	}
	return token, nil
}
