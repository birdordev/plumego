// Package password provides secure password hashing and strength validation.
//
// This package implements industry-standard password security using PBKDF2-HMAC-SHA512
// with configurable iterations and salt generation. It provides both hashing/verification
// and password strength validation.
//
// Features:
//   - PBKDF2-HMAC-SHA512 key derivation (default: 210,000 iterations)
//   - Automatic salt generation (16 bytes)
//   - Constant-time comparison to prevent timing attacks
//   - Password strength validation with customizable rules
//
// Example usage:
//
//	import "github.com/birdordev/plumego/security/password"
//
//	// Hash a password
//	hash, err := password.HashPassword("user-password-123")
//	if err != nil {
//		// Handle error
//	}
//
//	// Verify a password
//	if err := password.CheckPassword(hash, "user-password-123"); err != nil {
//		// Invalid password
//	}
//
//	// Validate password strength
//	config := password.PasswordStrengthConfig{
//		MinLength:        12,
//		RequireUppercase: true,
//		RequireLowercase: true,
//		RequireDigit:     true,
//		RequireSpecial:   true,
//	}
//	if !password.ValidatePasswordStrength("WeakPass", config) {
//		// Password doesn't meet requirements
//	}
package password

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

var (
	// ErrInvalidCost is returned when a password hash cost is not usable.
	ErrInvalidCost = errors.New("password: invalid cost")
	// ErrInvalidHash is returned when a stored password hash cannot be parsed.
	ErrInvalidHash = errors.New("password: invalid hash")
	// ErrPasswordMismatch is returned when a password does not match a stored hash.
	ErrPasswordMismatch = errors.New("password: mismatch")
	// ErrPasswordTooLong is returned when plaintext password input exceeds the hashing bound.
	ErrPasswordTooLong = errors.New("password: too long")
)

// PasswordStrengthConfig defines the configuration for password strength validation.
//
// This configuration allows you to specify the minimum requirements for passwords
// to ensure they meet security standards.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/password"
//
//	config := password.PasswordStrengthConfig{
//		MinLength:        12,
//		RequireUppercase: true,
//		RequireLowercase: true,
//		RequireDigit:     true,
//		RequireSpecial:   true,
//	}
//	if !password.ValidatePasswordStrength("MySecurePass123!", config) {
//		// Password doesn't meet requirements
//	}
type PasswordStrengthConfig struct {
	// MinLength is the minimum password length in Unicode code points.
	MinLength int

	// MaxLength is the maximum password length in Unicode code points.
	// A zero value disables the maximum bound.
	MaxLength int

	// RequireUppercase requires at least one uppercase letter
	RequireUppercase bool

	// RequireLowercase requires at least one lowercase letter
	RequireLowercase bool

	// RequireDigit requires at least one digit
	RequireDigit bool

	// RequireSpecial requires at least one special character
	RequireSpecial bool
}

// DefaultPasswordStrengthConfig returns the default password strength configuration.
//
// Defaults:
//   - MinLength: 8
//   - MaxLength: 1024
//   - RequireUppercase: true
//   - RequireLowercase: true
//   - RequireDigit: true
//   - RequireSpecial: false
//
// Example:
//
//	import "github.com/birdordev/plumego/security/password"
//
//	config := password.DefaultPasswordStrengthConfig()
//	if !password.ValidatePasswordStrength("MySecurePass123", config) {
//		// Password doesn't meet requirements
//	}
func DefaultPasswordStrengthConfig() PasswordStrengthConfig {
	return PasswordStrengthConfig{
		MinLength:        8,
		MaxLength:        1024,
		RequireUppercase: true,
		RequireLowercase: true,
		RequireDigit:     true,
		RequireSpecial:   false,
	}
}

// ValidatePasswordStrength checks if the password meets the required strength criteria.
//
// Example:
//
//	import "github.com/birdordev/plumego/security/password"
//
//	config := password.DefaultPasswordStrengthConfig()
//	if !password.ValidatePasswordStrength("MySecurePass123", config) {
//		// Password doesn't meet requirements
//	}
func ValidatePasswordStrength(password string, config PasswordStrengthConfig) bool {
	if config.MinLength < 0 || config.MaxLength < 0 {
		return false
	}
	length := 0
	for range password {
		length++
		if config.MaxLength > 0 && length > config.MaxLength {
			return false
		}
	}
	if length < config.MinLength {
		return false
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool

	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsDigit(char):
			hasDigit = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	// Check all required conditions
	if config.RequireUppercase && !hasUpper {
		return false
	}
	if config.RequireLowercase && !hasLower {
		return false
	}
	if config.RequireDigit && !hasDigit {
		return false
	}
	if config.RequireSpecial && !hasSpecial {
		return false
	}

	return true
}

// DefaultCost represents the default iteration count for password hashing.
// A higher value increases the computational cost for attackers attempting to
// brute-force hashed passwords.
//
// OWASP 2024 recommends at least 600,000 iterations for PBKDF2-SHA256 or
// 210,000 iterations for PBKDF2-SHA512 to achieve approximately 100ms of
// computation time on typical hardware. We use 210,000 as the default for
// PBKDF2-SHA512.
//
// Note: The previous value of 10,000 was too low for modern security standards.
// If you have existing hashed passwords, consider implementing a migration
// strategy to rehash passwords with the new cost when users log in.
const DefaultCost = 210_000

// MinimumCost represents the minimum accepted iteration count.
// Hash generation and verification reject stored costs below this bound.
const MinimumCost = 100_000

// MaximumCost is the largest accepted iteration count for hashing or verifying.
// It bounds attacker-controlled work when parsing stored password hashes.
const MaximumCost = 2_000_000

// MaxPasswordLength is the maximum plaintext password size accepted by hashing
// and verification entrypoints, in bytes.
const MaxPasswordLength = 4096

const (
	saltSize = 16

	// hashSize is the derived key length for new hashes (full SHA-512 output).
	hashSize = 64

	// hashSizeLegacy is the derived key length used by hashes produced before
	// the hashSize increase. CheckPassword accepts both sizes; HashPassword
	// always produces hashSize-byte hashes.
	hashSizeLegacy = 32
)

// HashPassword generates a salted hash of the password with the default cost.
func HashPassword(password string) (string, error) {
	return HashPasswordWithCost(password, DefaultCost)
}

// HashPasswordWithCost generates a salted hash of the password with the specified cost.
// The returned string has the format: "<cost>$<salt>$<hash>".
func HashPasswordWithCost(password string, cost int) (string, error) {
	if err := validateCost(cost); err != nil {
		return "", err
	}
	if err := validatePasswordLength(password); err != nil {
		return "", err
	}

	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	derived, err := deriveKey(password, salt, cost, hashSize)
	if err != nil {
		return "", fmt.Errorf("derive key: %w", err)
	}
	encodedSalt := base64.StdEncoding.EncodeToString(salt)
	encodedHash := base64.StdEncoding.EncodeToString(derived)

	return fmt.Sprintf("%d$%s$%s", cost, encodedSalt, encodedHash), nil
}

// CheckPassword compares a hashed password with its plaintext version.
//
// It accepts both 64-byte hashes (current default) and 32-byte hashes
// produced by earlier versions of HashPassword; both formats remain valid.
func CheckPassword(hashedPassword, password string) error {
	if err := validatePasswordLength(password); err != nil {
		return err
	}

	costText, rest, hasCost := strings.Cut(hashedPassword, "$")
	saltText, hashText, hasSalt := strings.Cut(rest, "$")
	if !hasCost || !hasSalt || strings.Contains(hashText, "$") {
		return ErrInvalidHash
	}

	cost, err := strconv.Atoi(costText)
	if err != nil {
		return fmt.Errorf("%w: invalid cost", ErrInvalidHash)
	}
	if err := validateCost(cost); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidHash, err)
	}

	salt, err := decodeStoredField(saltText, saltSize)
	if err != nil {
		return fmt.Errorf("%w: decode salt: %w", ErrInvalidHash, err)
	}
	if len(salt) != saltSize {
		return fmt.Errorf("%w: invalid salt length", ErrInvalidHash)
	}

	expectedHash, err := decodeStoredField(hashText, hashSize)
	if err != nil {
		return fmt.Errorf("%w: decode hash: %w", ErrInvalidHash, err)
	}

	// Accept both current (64-byte) and legacy (32-byte) derived key lengths.
	targetLen := len(expectedHash)
	if targetLen != hashSizeLegacy && targetLen != hashSize {
		return fmt.Errorf("%w: invalid hash length", ErrInvalidHash)
	}

	derived, keyErr := deriveKey(password, salt, cost, targetLen)
	if keyErr != nil {
		return fmt.Errorf("%w: derive key: %w", ErrInvalidHash, keyErr)
	}
	if subtle.ConstantTimeCompare(expectedHash, derived) == 1 {
		return nil
	}

	return ErrPasswordMismatch
}

// decodeStoredField bounds decoder allocations and preserves the standard
// base64 decoder's tolerance of CR and LF in previously stored values.
func decodeStoredField(value string, maxBytes int) ([]byte, error) {
	var encoded [((hashSize + 2) / 3) * 4]byte
	limit := base64.StdEncoding.EncodedLen(maxBytes)
	n := 0
	for i := 0; i < len(value); i++ {
		if value[i] == '\r' || value[i] == '\n' {
			continue
		}
		if n == limit {
			return nil, ErrInvalidHash
		}
		encoded[n] = value[i]
		n++
	}
	return base64.StdEncoding.AppendDecode(nil, encoded[:n])
}

func deriveKey(password string, salt []byte, cost, keyLen int) ([]byte, error) {
	return pbkdf2.Key(sha512.New, password, salt, cost, keyLen)
}

func validateCost(cost int) error {
	if cost < MinimumCost {
		return fmt.Errorf("%w: must be at least %d", ErrInvalidCost, MinimumCost)
	}
	if cost > MaximumCost {
		return fmt.Errorf("%w: must be at most %d", ErrInvalidCost, MaximumCost)
	}
	return nil
}

func validatePasswordLength(password string) error {
	if len(password) > MaxPasswordLength {
		return fmt.Errorf("%w: max %d bytes", ErrPasswordTooLong, MaxPasswordLength)
	}
	return nil
}
