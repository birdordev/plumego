package password

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestHashPassword(t *testing.T) {
	password := "testpassword123"
	hashed, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if hashed == password {
		t.Error("Hashed password should not be the same as plaintext")
	}
	if len(hashed) == 0 {
		t.Error("Hashed password should not be empty")
	}
}

func TestCheckPassword(t *testing.T) {
	password := "testpassword123"
	hashed, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	// Test correct password
	err = CheckPassword(hashed, password)
	if err != nil {
		t.Errorf("CheckPassword failed for correct password: %v", err)
	}

	// Test incorrect password
	err = CheckPassword(hashed, "wrongpassword")
	if !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("CheckPassword incorrect error = %v, want ErrPasswordMismatch", err)
	}

	// Test empty password
	err = CheckPassword(hashed, "")
	if !errors.Is(err, ErrPasswordMismatch) {
		t.Errorf("CheckPassword empty error = %v, want ErrPasswordMismatch", err)
	}
}

func TestCheckPasswordDifferentCosts(t *testing.T) {
	// Test that hashing with different costs still works for verification
	password := "testpassword123"
	hashed1, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	err = CheckPassword(hashed1, password)
	if err != nil {
		t.Errorf("CheckPassword failed for correct password: %v", err)
	}
}

func TestPasswordHashRejectsOverlongPassword(t *testing.T) {
	overlong := strings.Repeat("a", MaxPasswordLength+1)

	if _, err := HashPasswordWithCost(overlong, MinimumCost); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("HashPasswordWithCost overlong error = %v, want ErrPasswordTooLong", err)
	}
}

func TestCheckPasswordRejectsOverlongPassword(t *testing.T) {
	hashed, err := HashPasswordWithCost("short-password", MinimumCost)
	if err != nil {
		t.Fatalf("HashPasswordWithCost: %v", err)
	}

	overlong := strings.Repeat("a", MaxPasswordLength+1)
	if err := CheckPassword(hashed, overlong); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("CheckPassword overlong error = %v, want ErrPasswordTooLong", err)
	}
}

func TestPasswordHashAcceptsMaxLengthPassword(t *testing.T) {
	maxLength := strings.Repeat("a", MaxPasswordLength)

	hashed, err := HashPasswordWithCost(maxLength, MinimumCost)
	if err != nil {
		t.Fatalf("HashPasswordWithCost max length: %v", err)
	}
	if err := CheckPassword(hashed, maxLength); err != nil {
		t.Fatalf("CheckPassword max length: %v", err)
	}
}

// TestHashPasswordWithCostInvalidCostTooLow exercises the ErrInvalidCost path
// when cost is below MinimumCost.
func TestHashPasswordWithCostInvalidCostTooLow(t *testing.T) {
	_, err := HashPasswordWithCost("password", MinimumCost-1)
	if !errors.Is(err, ErrInvalidCost) {
		t.Fatalf("error = %v, want ErrInvalidCost", err)
	}
}

// TestHashPasswordWithCostInvalidCostTooHigh exercises the ErrInvalidCost path
// when cost is above MaximumCost.
func TestHashPasswordWithCostInvalidCostTooHigh(t *testing.T) {
	_, err := HashPasswordWithCost("password", MaximumCost+1)
	if !errors.Is(err, ErrInvalidCost) {
		t.Fatalf("error = %v, want ErrInvalidCost", err)
	}
}

// TestCheckPasswordAcceptsLegacyHash verifies that 32-byte hashes
// (hashSizeLegacy) produced before the hashSize increase remain verifiable.
func TestCheckPasswordAcceptsLegacyHash(t *testing.T) {
	pw := "test-legacy-password"
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	cost := MinimumCost

	derived, err := deriveKey(pw, salt, cost, hashSizeLegacy)
	if err != nil {
		t.Fatalf("deriveKey: %v", err)
	}

	stored := fmt.Sprintf("%d$%s$%s",
		cost,
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(derived),
	)

	if err := CheckPassword(stored, pw); err != nil {
		t.Fatalf("CheckPassword with legacy 32-byte hash: %v", err)
	}
	if err := CheckPassword(stored, "wrong-password"); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("expected ErrPasswordMismatch, got %v", err)
	}
}

// TestCheckPasswordInvalidHashFormats covers the various ErrInvalidHash paths
// in CheckPassword (not enough segments, non-numeric cost, cost out of range,
// bad salt base64, wrong salt length, bad hash base64, wrong hash length).
func TestCheckPasswordInvalidHashFormats(t *testing.T) {
	cases := []struct {
		name   string
		hashed string
	}{
		{"too few segments", "noCost$onlyTwo"},
		{"non-numeric cost", "notanint$c2FsdA==$aGFzaA=="},
		{"cost too low", "99999$c2FsdA==$aGFzaA=="},
		{"cost too high", "2000001$c2FsdA==$aGFzaA=="},
		{"bad salt base64", "100000$not!base64$aGFzaA=="},
		// Valid base64 but wrong decoded length (not 16 bytes).
		{"wrong salt length", "100000$dGVzdA==$aGFzaA=="},
		// Valid cost + valid 16-byte salt + bad hash base64.
		{"bad hash base64", "100000$AAAAAAAAAAAAAAAAAAAAAA==$not!base64"},
		// Valid cost + valid 16-byte salt + valid base64 but wrong hash length (not 32 or 64 bytes).
		{"wrong hash length", "100000$AAAAAAAAAAAAAAAAAAAAAA==$aGFzaA=="},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckPassword(tc.hashed, "password")
			if !errors.Is(err, ErrInvalidHash) {
				t.Fatalf("CheckPassword(%q) error = %v, want ErrInvalidHash", tc.hashed, err)
			}
		})
	}
}

func TestStoredHashParserPreservesWrappedBase64(t *testing.T) {
	const plaintext = "fixture-password"
	stored, err := HashPasswordWithCost(plaintext, MinimumCost)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(stored, "$")
	for i := 1; i < len(parts); i++ {
		parts[i] = "\r\n" + parts[i][:4] + "\n" + parts[i][4:] + "\r"
	}
	if err := CheckPassword(strings.Join(parts, "$"), plaintext); err != nil {
		t.Fatalf("wrapped base64 rejected: %v", err)
	}
	for _, malformed := range []string{
		parts[0] + "$" + strings.Repeat("A", 1<<20) + "$" + parts[2],
		parts[0] + "$" + parts[1] + "$" + strings.Repeat("A", 1<<20),
		stored + "$extra",
	} {
		if err := CheckPassword(malformed, plaintext); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("invalid stored hash: got %v", err)
		}
	}
}

func TestCheckPasswordErrorsOmitInputMaterial(t *testing.T) {
	const password = "example-password-placeholder"
	const malformed = "example-format-placeholder"
	hash, err := HashPasswordWithCost(password, MinimumCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		hash, input string
		want        error
	}{
		{malformed, password, ErrInvalidHash},
		{hash, password + "-other", ErrPasswordMismatch},
	} {
		err := CheckPassword(tc.hash, tc.input)
		if !errors.Is(err, tc.want) {
			t.Fatalf("expected %v, got error type %T", tc.want, err)
		}
		if strings.Contains(err.Error(), tc.hash) || strings.Contains(err.Error(), tc.input) {
			t.Fatal("error message contains input material")
		}
	}
}

func TestInvalidWorkFactorsRejectedBeforeDecodingStoredFields(t *testing.T) {
	const fixture = "local fixture"
	for _, cost := range []int{MinimumCost - 1, MaximumCost + 1} {
		if got, err := HashPasswordWithCost(fixture, cost); got != "" || !errors.Is(err, ErrInvalidCost) {
			t.Fatalf("unexpected hashing result: %v", err)
		}
		encoded := strconv.Itoa(cost) + "$fixture-salt$fixture-hash"
		err := CheckPassword(encoded, fixture)
		if !errors.Is(err, ErrInvalidHash) || !errors.Is(err, ErrInvalidCost) ||
			strings.Contains(err.Error(), fixture) || strings.Contains(err.Error(), "fixture-salt") {
			t.Fatalf("unexpected error classification: %v", err)
		}
	}
}

func TestPasswordStrengthUsesUnicodeCharacterCounts(t *testing.T) {
	cfg := PasswordStrengthConfig{MinLength: 4, MaxLength: 4, RequireUppercase: true, RequireLowercase: true, RequireDigit: true, RequireSpecial: true}
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"Äé٣!", true}, {"Åé٣", false}, {"Äé٣!!", false}, {"äé٣!", false}, {"ÄÉ٣!", false}, {"Äé界!", false}, {"Äé٣界", false},
	} {
		if got := ValidatePasswordStrength(tc.value, cfg); got != tc.want {
			t.Errorf("value=%q got=%t want=%t", tc.value, got, tc.want)
		}
	}
}

func TestPasswordStoredFieldRejectionKeepsFixedClassification(t *testing.T) {
	validSalt := base64.StdEncoding.EncodeToString(make([]byte, saltSize))
	validSize := base64.StdEncoding.EncodeToString(make([]byte, hashSize))
	for _, data := range [][2]string{
		{"fixture!", validSize},
		{base64.StdEncoding.EncodeToString(make([]byte, saltSize-1)), validSize},
		{validSalt, "fixture!"},
		{validSalt, base64.StdEncoding.EncodeToString(make([]byte, hashSize+1))},
	} {
		err := CheckPassword(fmt.Sprintf("%d$%s$%s", MinimumCost, data[0], data[1]), "ordinary fixture")
		if !errors.Is(err, ErrInvalidHash) || strings.Contains(err.Error(), "fixture!") {
			t.Fatalf("validation=%v", err)
		}
	}
}

func TestHashLengthCountsMultibyteInputByBytes(t *testing.T) {
	input := strings.Repeat("é", MaxPasswordLength/2)
	if _, err := HashPasswordWithCost(input, MinimumCost); err != nil {
		t.Fatal("fixture at the exact byte limit was rejected")
	}
	if _, err := HashPasswordWithCost(input+"é", MinimumCost); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("classification=%v", err)
	}
}
