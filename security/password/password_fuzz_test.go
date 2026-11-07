package password

import (
	"errors"
	"strings"
	"testing"
)

// FuzzPasswordHashVerifyRoundTrip verifies the hash/check contract for
// arbitrary password inputs: hashing succeeds only for in-range lengths, a
// correct password always checks out, and a wrong password never does.
// The minimum cost keeps each fuzz iteration fast while exercising the same
// code paths as the default cost.
func FuzzPasswordHashVerifyRoundTrip(f *testing.F) {
	seeds := []string{
		"",
		"a",
		"password",
		"p@ss w0rd 密码",
		strings.Repeat("x", MaxPasswordLength),
		strings.Repeat("x", MaxPasswordLength+1),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, password string) {
		hash, err := HashPasswordWithCost(password, MinimumCost)
		if err != nil {
			if !errors.Is(err, ErrPasswordTooLong) || len(password) <= MaxPasswordLength {
				t.Fatalf("HashPasswordWithCost(%d bytes) = %v, want success or ErrPasswordTooLong", len(password), err)
			}
			return
		}
		if err := CheckPassword(hash, password); err != nil {
			t.Fatalf("round trip failed for %d-byte password: %v", len(password), err)
		}
		if wrong := password + "x"; len(wrong) <= MaxPasswordLength {
			if err := CheckPassword(hash, wrong); !errors.Is(err, ErrPasswordMismatch) {
				t.Fatalf("expected ErrPasswordMismatch for wrong password, got %v", err)
			}
		}
	})
}

// FuzzPasswordCheckRejectsMalformedHash feeds arbitrary hash strings to
// CheckPassword. Any input that is not a valid hash for the fixed test
// password must fail closed without panicking.
func FuzzPasswordCheckRejectsMalformedHash(f *testing.F) {
	seeds := []string{
		"",
		"garbage",
		"$",
		"0$AAAA$AAAA",
		"210000$AAAA$AAAA",
		"210000$c2FsdA==$AAAA",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, hashed string) {
		if err := CheckPassword(hashed, "password"); err == nil {
			t.Fatalf("CheckPassword accepted malformed hash %q", hashed)
		}
	})
}
