package log

import (
	"bytes"
	"strings"
	"testing"
)

func TestSafeMapCapacity(t *testing.T) {
	tests := []struct {
		name    string
		lengths []int
		want    int
	}{
		{
			name:    "empty",
			lengths: nil,
			want:    0,
		},
		{
			name:    "sums lengths",
			lengths: []int{2, 3, 5},
			want:    10,
		},
		{
			name:    "rejects negative length",
			lengths: []int{1, -1},
			want:    0,
		},
		{
			name:    "falls back on overflow",
			lengths: []int{maxMapCapacity, 1},
			want:    0,
		},
		{
			name:    "allows max capacity",
			lengths: []int{maxMapCapacity - 1, 1},
			want:    maxMapCapacity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeMapCapacity(tt.lengths...); got != tt.want {
				t.Fatalf("safeMapCapacity(%v) = %d, want %d", tt.lengths, got, tt.want)
			}
		})
	}
}

func TestMergeFieldsKeepsOverridePrecedence(t *testing.T) {
	got := mergeFields(Fields{
		"base":     "yes",
		"override": "base",
	}, Fields{
		"extra":    "yes",
		"override": "extra",
	})

	for key, want := range (Fields{
		"base":     "yes",
		"extra":    "yes",
		"override": "extra",
	}) {
		if got[key] != want {
			t.Fatalf("mergeFields()[%s] = %v, want %v in %#v", key, got[key], want, got)
		}
	}
}

func TestMergeFieldsKeepsLaterPrecedence(t *testing.T) {
	got := mergeFields(
		Fields{"first": "yes", "override": "first"},
		Fields{"second": "yes", "override": "second"},
		Fields{"third": "yes", "override": "third"},
	)

	for key, want := range (Fields{
		"first":    "yes",
		"second":   "yes",
		"third":    "yes",
		"override": "third",
	}) {
		if got[key] != want {
			t.Fatalf("mergeFields()[%s] = %v, want %v in %#v", key, got[key], want, got)
		}
	}
}

func TestTextLoggerFieldPrecedenceAcrossParentChildAndCall(t *testing.T) {
	var output bytes.Buffer
	parent := NewLogger(LoggerConfig{Output: &output, Fields: Fields{"scope": "parent"}})
	child := parent.WithFields(Fields{"scope": "child"})
	child.Info("call", Fields{"scope": "call"})
	child.Info("next")
	parent.Info("original")
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	want := []string{"call", "child", "parent"}
	if len(lines) != len(want) {
		t.Fatalf("lines=%q", lines)
	}
	for i, scope := range want {
		if strings.Count(lines[i], "scope=") != 1 || !strings.Contains(lines[i], "scope="+scope) {
			t.Errorf("line=%q want scope=%q", lines[i], scope)
		}
	}
}
