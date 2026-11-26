package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"with-cloud-manager/internal/billing"
)

func TestAllocationPolicyRejectsAmbiguousJSON(t *testing.T) {
	for _, body := range []string{
		`null`,
		`{"rules":[],"Rules":[]}`,
		`{"rules":[{"tags":{"env":"test","env":"prod"},"shares":[{"center":"app","weight":10000}]}]}`,
		`{"default":[{"center":"app","Weight":5000,"weight":10000}]}`,
		`{} {}`,
		`{"future_field": true}`,
	} {
		path := filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readAllocationPolicy(path); !errors.Is(err, billing.ErrInvalid) {
			t.Fatalf("invalid policy accepted: %s (%v)", body, err)
		}
	}
	valid := `{"rules":[{"tags":{"ENV":"test","env":"prod"},"shares":[{"center":"app","weight":10000}]}]}`
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if parsed, err := readAllocationPolicy(path); err != nil || len(parsed.Rules) != 1 || len(parsed.Rules[0].Tags) != 2 {
		t.Fatalf("valid case-sensitive tags rejected: %+v (%v)", parsed, err)
	}
}
