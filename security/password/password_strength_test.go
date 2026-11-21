package password

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestValidatePasswordStrength(t *testing.T) {
	tests := []struct {
		name     string
		password string
		config   PasswordStrengthConfig
		expected bool
	}{
		{
			name:     "valid password with all requirements",
			password: "StrongPass1!",
			config: PasswordStrengthConfig{
				MinLength:        8,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   true,
			},
			expected: true,
		},
		{
			name:     "password too short",
			password: "Short1!",
			config: PasswordStrengthConfig{
				MinLength:        8,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   true,
			},
			expected: false,
		},
		{
			name:     "missing uppercase",
			password: "lowercase1!",
			config: PasswordStrengthConfig{
				MinLength:        8,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   true,
			},
			expected: false,
		},
		{
			name:     "missing lowercase",
			password: "UPPERCASE1!",
			config: PasswordStrengthConfig{
				MinLength:        8,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   true,
			},
			expected: false,
		},
		{
			name:     "missing digit",
			password: "StrongPass!",
			config: PasswordStrengthConfig{
				MinLength:        8,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   true,
			},
			expected: false,
		},
		{
			name:     "missing special (but allowed)",
			password: "StrongPass1",
			config: PasswordStrengthConfig{
				MinLength:        8,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   false,
			},
			expected: true,
		},
		{
			name:     "missing special (required)",
			password: "StrongPass1",
			config: PasswordStrengthConfig{
				MinLength:        8,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   true,
			},
			expected: false,
		},
		{
			name:     "empty password",
			password: "",
			config: PasswordStrengthConfig{
				MinLength:        8,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   true,
			},
			expected: false,
		},
		{
			name:     "no requirements",
			password: "any",
			config: PasswordStrengthConfig{
				MinLength:        0,
				RequireUppercase: false,
				RequireLowercase: false,
				RequireDigit:     false,
				RequireSpecial:   false,
			},
			expected: true,
		},
		{
			name:     "unicode characters",
			password: "StrongPass1!中文",
			config: PasswordStrengthConfig{
				MinLength:        8,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   true,
			},
			expected: true,
		},
		{
			name:     "unicode length counts runes",
			password: "密码",
			config: PasswordStrengthConfig{
				MinLength:        3,
				RequireUppercase: false,
				RequireLowercase: false,
				RequireDigit:     false,
				RequireSpecial:   false,
			},
			expected: false,
		},
		{
			name:     "max length rejects oversized password",
			password: "StrongPass1!",
			config: PasswordStrengthConfig{
				MinLength:        8,
				MaxLength:        10,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   true,
			},
			expected: false,
		},
		{
			name:     "negative max length fails closed",
			password: "StrongPass1!",
			config: PasswordStrengthConfig{
				MinLength:        8,
				MaxLength:        -1,
				RequireUppercase: true,
				RequireLowercase: true,
				RequireDigit:     true,
				RequireSpecial:   true,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ValidatePasswordStrength(tt.password, tt.config)
			if result != tt.expected {
				t.Errorf("ValidatePasswordStrength(%q, %+v) = %v, expected %v",
					tt.password, tt.config, result, tt.expected)
			}
		})
	}
}

func TestDefaultPasswordStrengthConfig(t *testing.T) {
	config := DefaultPasswordStrengthConfig()

	if config.MinLength != 8 {
		t.Errorf("MinLength = %d, expected 8", config.MinLength)
	}
	if config.MaxLength != 1024 {
		t.Errorf("MaxLength = %d, expected 1024", config.MaxLength)
	}
	if !config.RequireUppercase {
		t.Error("RequireUppercase should be true")
	}
	if !config.RequireLowercase {
		t.Error("RequireLowercase should be true")
	}
	if !config.RequireDigit {
		t.Error("RequireDigit should be true")
	}
	if config.RequireSpecial {
		t.Error("RequireSpecial should be false")
	}
}

func TestHashPasswordWithCost(t *testing.T) {
	// Test with invalid cost
	_, err := HashPasswordWithCost("password", 0)
	if !errors.Is(err, ErrInvalidCost) {
		t.Errorf("HashPasswordWithCost cost 0 error = %v, want ErrInvalidCost", err)
	}

	// Test with negative cost
	_, err = HashPasswordWithCost("password", -1)
	if !errors.Is(err, ErrInvalidCost) {
		t.Errorf("HashPasswordWithCost negative cost error = %v, want ErrInvalidCost", err)
	}

	_, err = HashPasswordWithCost("password", MinimumCost-1)
	if !errors.Is(err, ErrInvalidCost) {
		t.Errorf("HashPasswordWithCost below minimum error = %v, want ErrInvalidCost", err)
	}

	_, err = HashPasswordWithCost("password", MaximumCost+1)
	if !errors.Is(err, ErrInvalidCost) {
		t.Errorf("HashPasswordWithCost oversized cost error = %v, want ErrInvalidCost", err)
	}

	// Test with valid cost
	hash, err := HashPasswordWithCost("password", MinimumCost)
	if err != nil {
		t.Errorf("HashPasswordWithCost failed: %v", err)
	}
	if hash == "" {
		t.Error("HashPasswordWithCost returned empty hash")
	}

	// Verify the hash format
	parts := strings.Split(hash, "$")
	if len(parts) != 3 {
		t.Errorf("Invalid hash format: %s", hash)
	}
}

func TestCheckPasswordRejectsBelowMinimumCost(t *testing.T) {
	salt := base64.StdEncoding.EncodeToString(make([]byte, saltSize))
	hash := base64.StdEncoding.EncodeToString(make([]byte, hashSize))
	stored := strings.Join([]string{"99999", salt, hash}, "$")

	err := CheckPassword(stored, "password")
	if !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("CheckPassword low cost error = %v, want ErrInvalidHash", err)
	}
	if !errors.Is(err, ErrInvalidCost) {
		t.Fatalf("CheckPassword low cost error = %v, want ErrInvalidCost cause", err)
	}
}

func TestCheckPasswordInvalidFormat(t *testing.T) {
	tests := []struct {
		name string
		hash string
	}{
		{
			name: "missing parts",
			hash: "10$salt",
		},
		{
			name: "invalid cost",
			hash: "abc$salt$hash",
		},
		{
			name: "invalid salt base64",
			hash: "10$invalid!!!$hash",
		},
		{
			name: "invalid hash base64",
			hash: "10$salt$invalid!!!",
		},
		{
			name: "invalid salt length",
			hash: "10$" + base64.StdEncoding.EncodeToString([]byte("short")) + "$" + base64.StdEncoding.EncodeToString(make([]byte, hashSize)),
		},
		{
			name: "invalid hash length",
			hash: "10$" + base64.StdEncoding.EncodeToString(make([]byte, saltSize)) + "$" + base64.StdEncoding.EncodeToString([]byte("short")),
		},
		{
			name: "oversized cost",
			hash: strconv.Itoa(MaximumCost+1) + "$" + base64.StdEncoding.EncodeToString(make([]byte, saltSize)) + "$" + base64.StdEncoding.EncodeToString(make([]byte, hashSize)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckPassword(tt.hash, "password")
			if !errors.Is(err, ErrInvalidHash) {
				t.Errorf("CheckPassword error = %v, want ErrInvalidHash", err)
			}
		})
	}
}

func TestDeriveKey(t *testing.T) {
	password := "test"
	salt := []byte("saltsalt")

	// Different iteration counts produce different results.
	r1, err := deriveKey(password, salt, 1, 32)
	if err != nil {
		t.Fatalf("deriveKey iter=1: %v", err)
	}
	r2, err := deriveKey(password, salt, 100, 32)
	if err != nil {
		t.Fatalf("deriveKey iter=100: %v", err)
	}
	if len(r1) != 32 {
		t.Errorf("expected len=32, got %d", len(r1))
	}
	if len(r2) != 32 {
		t.Errorf("expected len=32, got %d", len(r2))
	}
	if string(r1) == string(r2) {
		t.Error("different iteration counts must produce different results")
	}

	// Requested key length is honoured.
	r3, err := deriveKey(password, salt, 10, 16)
	if err != nil {
		t.Fatalf("deriveKey keyLen=16: %v", err)
	}
	if len(r3) != 16 {
		t.Errorf("expected len=16, got %d", len(r3))
	}

	// Derivation is deterministic.
	ra, _ := deriveKey(password, salt, 5, 32)
	rb, _ := deriveKey(password, salt, 5, 32)
	if string(ra) != string(rb) {
		t.Error("deriveKey must be deterministic for identical inputs")
	}
}

func TestStrengthZeroValueAndExplicitSingleRequirement(t *testing.T) {
	for _, tc := range []struct {
		value string
		cfg   PasswordStrengthConfig
		want  bool
	}{
		{"", PasswordStrengthConfig{}, true},
		{"a", PasswordStrengthConfig{}, true},
		{"é", PasswordStrengthConfig{MinLength: 2}, false},
		{"é", PasswordStrengthConfig{MinLength: 1, MaxLength: 1}, true},
		{"A", PasswordStrengthConfig{RequireDigit: true}, false},
		{"1", PasswordStrengthConfig{RequireDigit: true}, true},
	} {
		if got := ValidatePasswordStrength(tc.value, tc.cfg); got != tc.want {
			t.Fatalf("value=%q config=%+v valid=%t want=%t", tc.value, tc.cfg, got, tc.want)
		}
	}
}
