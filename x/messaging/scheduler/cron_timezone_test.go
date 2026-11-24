package scheduler

import (
	"testing"
	"time"
)

func TestCronNextAcrossOffsetChanges(t *testing.T) {
	for _, tc := range []struct {
		name, zone, expr, after, want string
	}{
		{"first repeated hour", "America/New_York", "30 1 * * *", "2026-11-01T05:00:00Z", "2026-11-01T05:30:00Z"},
		{"second repeated hour", "America/New_York", "30 1 * * *", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"},
		{"search starts in second hour", "America/New_York", "30 1 * * *", "2026-11-01T06:12:00Z", "2026-11-01T06:30:00Z"},
		{"missing hour", "America/New_York", "30 2 * * *", "2026-03-08T06:50:00Z", "2026-03-09T06:30:00Z"},
		{"half hour fold", "Australia/Lord_Howe", "45 1 * * *", "2026-04-04T14:45:00Z", "2026-04-04T15:15:00Z"},
		{"half hour gap", "Australia/Lord_Howe", "15 2 * * *", "2026-10-03T14:59:59Z", "2026-10-04T15:15:00Z"},
		{"skipped civil date", "Pacific/Apia", "0 9 30 * *", "2011-12-29T20:00:00Z", "2012-01-29T19:00:00Z"},
		{"leap day", "UTC", "15 9 29 2 *", "2027-02-28T23:59:59Z", "2028-02-29T09:15:00Z"},
		{"nonleap century", "UTC", "15 9 29 2 *", "2096-02-29T09:15:00Z", "2104-02-29T09:15:00Z"},
		{"short month skipped", "UTC", "0 17 31 * *", "2026-01-31T17:00:00Z", "2026-03-31T17:00:00Z"},
		{"month end with local offset", "Asia/Tokyo", "30 0 31 * *", "2026-01-30T16:00:00Z", "2026-03-30T15:30:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parse := func(raw string) time.Time {
				t.Helper()
				stamp, err := time.Parse(time.RFC3339, raw)
				if err != nil {
					t.Fatal(err)
				}
				return stamp
			}
			zone, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := ParseCronSpecWithLocation(tc.expr, zone)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := spec.Next(parse(tc.after)), parse(tc.want); !got.Equal(want) {
				t.Fatalf("next = %s (%s UTC), want %s", got, got.UTC(), want)
			}
		})
	}
}
