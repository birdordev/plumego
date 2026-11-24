package main

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const maxZoneRulesBytes = 64 << 10

func loadScheduleZone(zone string) ([]byte, error) {
	if len(zone) > 128 || zone == "." || zone == "Local" ||
		!fs.ValidPath(zone) || strings.ContainsAny(zone, "\\:") ||
		strings.HasPrefix(zone, "right/") || strings.HasPrefix(zone, "posix/") {
		return nil, errInvalidTask
	}
	if zone == "UTC" {
		return []byte{}, nil
	}
	if archive := os.Getenv("SCHEDULER_TZDATA_ZIP"); archive != "" {
		return zoneFromZip(archive, zone)
	}
	if source := os.Getenv("ZONEINFO"); source != "" {
		var data []byte
		var err error
		if strings.HasSuffix(source, ".zip") {
			data, err = zoneFromZip(source, zone)
		} else {
			data, err = zoneFromDirectory(source, zone)
		}
		if err == nil {
			if _, err := scheduleLocation(zone, data); err == nil {
				return data, nil
			}
		}
	}
	if data, err := zoneFromDirectory("/usr/share/zoneinfo", zone); err == nil {
		return data, nil
	}
	data, err := zoneFromZip(filepath.Join(runtime.GOROOT(), "lib", "time", "zoneinfo.zip"), zone)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errInvalidTask
	}
	return data, err
}

func zoneFromDirectory(path, zone string) ([]byte, error) {
	file, err := os.Open(filepath.Join(path, filepath.FromSlash(zone)))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errInvalidTask
	}
	return readZoneRules(file)
}

func zoneFromZip(path, zone string) ([]byte, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != zone {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer entry.Close()
		return readZoneRules(entry)
	}
	return nil, errInvalidTask
}

func readZoneRules(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxZoneRulesBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxZoneRulesBytes {
		return nil, errInvalidTask
	}
	return data, nil
}

func scheduleLocation(zone string, data []byte) (*time.Location, error) {
	if zone == "UTC" && len(data) == 0 {
		return time.UTC, nil
	}
	return time.LoadLocationFromTZData(zone, data)
}

func pinExistingScheduleZones(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT time_zone FROM schedules WHERE time_zone <> 'UTC'")
	if err != nil {
		return err
	}
	defer rows.Close()
	var zones []string
	for rows.Next() {
		var zone string
		if err := rows.Scan(&zone); err != nil {
			return err
		}
		zones = append(zones, zone)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	snapshots := make([][]byte, len(zones))
	for i, zone := range zones {
		data, err := loadScheduleZone(zone)
		if err != nil {
			return err
		}
		if _, err := scheduleLocation(zone, data); err != nil {
			return err
		}
		snapshots[i] = data
	}
	if len(zones) > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE schedules s SET zone_rules = z.rules
			FROM unnest($1::text[], $2::bytea[]) z(name, rules) WHERE s.time_zone = z.name`, zones, snapshots); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `ALTER TABLE schedules ADD CONSTRAINT scheduler_zone_snapshot
		CHECK ((time_zone = 'UTC') = (octet_length(zone_rules) = 0))`)
	return err
}
