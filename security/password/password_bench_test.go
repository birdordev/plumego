package password

import (
	"errors"
	"strings"
	"testing"
)

func BenchmarkHashPassword(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = HashPassword("correct-horse-battery-staple")
	}
}

func BenchmarkHashPasswordWithCost(b *testing.B) {
	for _, cost := range []int{MinimumCost, DefaultCost, MaximumCost} {
		b.Run(costLabel(cost), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = HashPasswordWithCost("correct-horse-battery-staple", cost)
			}
		})
	}
}

func BenchmarkCheckPassword(b *testing.B) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		b.Fatalf("setup: HashPassword failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CheckPassword(hash, "correct-horse-battery-staple")
	}
}

func BenchmarkCheckPasswordInvalid(b *testing.B) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		b.Fatalf("setup: HashPassword failed: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = CheckPassword(hash, "wrong-password")
	}
}

func BenchmarkValidatePasswordStrength(b *testing.B) {
	cfg := DefaultPasswordStrengthConfig()
	passwords := []string{
		"short",
		"correct-horse-battery-staple",
		"P@ssw0rd123!",
	}

	for _, pw := range passwords {
		b.Run(pw[:len(pw)/2+1], func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = ValidatePasswordStrength(pw, cfg)
			}
		})
	}
}

func costLabel(cost int) string {
	switch cost {
	case MinimumCost:
		return "min"
	case DefaultCost:
		return "default"
	case MaximumCost:
		return "max"
	default:
		return "custom"
	}
}

func BenchmarkMalformedStoredHash(b *testing.B) {
	for _, tc := range []struct {
		name string
		size int
	}{{"1KiB", 1 << 10}, {"1MiB", 1 << 20}} {
		b.Run(tc.name, func(b *testing.B) {
			stored := "210000$" + strings.Repeat("A", tc.size) + "$AA=="
			b.ReportAllocs()
			for b.Loop() {
				if err := CheckPassword(stored, "fixture"); !errors.Is(err, ErrInvalidHash) {
					b.Fatal(err)
				}
			}
		})
	}
}
