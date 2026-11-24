package main

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const exampleZone = "America/New_York"

func zoneArchive(t *testing.T, offset int32) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "zoneinfo.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	archive := zip.NewWriter(f)
	entry, err := archive.Create(exampleZone)
	if err != nil {
		t.Fatal(err)
	}
	// 构造只含固定 UTC 偏移的 TZif，以便独立模拟两版规则。
	data := make([]byte, 54)
	copy(data, "TZif")
	binary.BigEndian.PutUint32(data[36:], 1)
	binary.BigEndian.PutUint32(data[40:], 4)
	binary.BigEndian.PutUint32(data[44:], uint32(offset))
	copy(data[50:], "FIX\x00")
	if _, err := entry.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScheduleZoneKeepsLegacyZoneInfo(t *testing.T) {
	t.Setenv("SCHEDULER_TZDATA_ZIP", "")
	t.Setenv("ZONEINFO", zoneArchive(t, 7200))
	rules, err := loadScheduleZone(exampleZone)
	if err != nil {
		t.Fatal(err)
	}
	zone, err := scheduleLocation(exampleZone, rules)
	if err != nil {
		t.Fatal(err)
	}
	if _, offset := time.Now().In(zone).Zone(); offset != 7200 {
		t.Fatalf("ZONEINFO offset = %d", offset)
	}
}

func TestScheduleZoneInputAndArchiveLimit(t *testing.T) {
	t.Setenv("SCHEDULER_TZDATA_ZIP", zoneArchive(t, 3600))
	for _, zone := range []string{"", ".", "Local", "../UTC", "A/../UTC", "\\UTC", "C:UTC", "right/UTC", "Africa/Cairo"} {
		if _, err := loadScheduleZone(zone); !errors.Is(err, errInvalidTask) {
			t.Errorf("unexpected response for %q: %v", zone, err)
		}
	}
	if _, err := readZoneRules(strings.NewReader(strings.Repeat("x", maxZoneRulesBytes+1))); !errors.Is(err, errInvalidTask) {
		t.Fatalf("oversized rule: %v", err)
	}
}
