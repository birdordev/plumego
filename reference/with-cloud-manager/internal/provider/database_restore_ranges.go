package provider

import (
	"cmp"
	"context"
	"slices"
	"time"
)

const MaxDatabaseRestoreWindows = 512

const (
	DatabaseRestoreInstanceWindow = "instance"
	DatabaseRestoreToSourceWindow = "source_instance"
)

type DatabaseRestoreWindow struct {
	Start time.Time `json:"start_at"`
	End   time.Time `json:"end_at"`
}

type DatabaseRestoreWindowReader interface {
	ReadDatabaseRestoreWindows(context.Context, Scope, string, DatabaseDetails) ([]DatabaseRestoreWindow, error)
}

func DatabaseRestoreWindowScope(vendor, id string, db DatabaseDetails) string {
	if !ValidDatabaseBackupTarget(vendor, id, db) || (vendor == AlibabaECS && db.Engine == "sqlserver") {
		return ""
	}
	if db.Product == TencentSQLServer {
		return DatabaseRestoreToSourceWindow
	}
	return DatabaseRestoreInstanceWindow
}

func NormalizeDatabaseRestoreWindows(windows []DatabaseRestoreWindow) error {
	if windows == nil || len(windows) > MaxDatabaseRestoreWindows {
		return &Failure{Kind: InvalidResponse}
	}
	for i, window := range windows {
		start, end := window.Start.UTC(), window.End.UTC()
		if start.Year() < 1970 || end.Year() > 9999 || end.Before(start) {
			return &Failure{Kind: InvalidResponse}
		}
		windows[i] = DatabaseRestoreWindow{Start: start, End: end}
	}
	slices.SortFunc(windows, func(a, b DatabaseRestoreWindow) int {
		return cmp.Or(a.Start.Compare(b.Start), a.End.Compare(b.End))
	})
	return nil
}
