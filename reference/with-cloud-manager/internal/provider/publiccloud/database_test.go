package publiccloud

import (
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestDatabaseCursor(t *testing.T) {
	for _, test := range []struct {
		cursor string
		want   regionalInstancePosition
	}{
		{"", regionalInstancePosition{}},
		{"0:50:51", regionalInstancePosition{0, 50, 51}},
		{"1:0:0", regionalInstancePosition{1, 0, 0}},
		{"2:100:120", regionalInstancePosition{2, 100, 120}},
	} {
		got, err := parseRegionalInstancePosition(test.cursor, 3, regionalInstancePageSize)
		if err != nil || got != test.want {
			t.Fatalf("cursor %q got %+v, %v; want %+v", test.cursor, got, err, test.want)
		}
	}
	for _, cursor := range []string{"0:0:0", "1", "-1:0:0", "01:0:0", "1:51:101", "3:0:0", "1:50:50", "1:50:500001"} {
		if _, err := parseRegionalInstancePosition(cursor, 3, regionalInstancePageSize); !provider.IsFailure(err, provider.ExpiredCursor) {
			t.Fatalf("malformed database cursor %q: %v", cursor, err)
		}
	}
}

func TestDatabasePageRequiresCompleteCount(t *testing.T) {
	zero, total := 0, 51
	if err := checkRegionalInstancePage(0, regionalInstancePageSize, &zero, []provider.Object{}); err != nil {
		t.Fatal(err)
	}
	if err := checkRegionalInstancePage(0, regionalInstancePageSize, &zero, nil); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("missing collection became proof of absence: %v", err)
	}
	if err := checkRegionalInstancePage(50, regionalInstancePageSize, &total, []provider.Object{}); !provider.IsFailure(err, provider.ExpiredCursor) {
		t.Fatalf("truncated page looked complete: %v", err)
	}
}
