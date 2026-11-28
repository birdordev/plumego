package provider

import (
	"cmp"
	"context"
	"slices"
	"time"
)

const MaxDatabaseBackups = 5000
const DatabaseBackupWindow = 30 * 24 * time.Hour

type DatabaseBackup struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	Mode      string     `json:"mode"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	SizeBytes *int64     `json:"size_bytes,omitempty,string"`
}

type DatabaseBackupReader interface {
	ReadDatabaseBackups(context.Context, Scope, string, DatabaseDetails, time.Time) ([]DatabaseBackup, error)
}

func ValidDatabaseBackupTarget(vendor, id string, db DatabaseDetails) bool {
	if !databaseAssetID.MatchString(id) || !databaseEngine.MatchString(db.Engine) {
		return false
	}
	switch vendor {
	case TencentCVM:
		if TencentDatabaseProduct(id) != db.Product {
			return false
		}
		return (db.Product == TencentCDB && db.Engine == "mysql") ||
			(db.Product == TencentPostgreSQL && db.Engine == "postgresql") ||
			(db.Product == TencentSQLServer && db.Engine == "sqlserver")
	case AlibabaECS:
		return db.Product == AlibabaRDS && slices.Contains([]string{"mysql", "postgresql", "sqlserver", "mariadb"}, db.Engine)
	case HuaweiECS:
		return db.Product == HuaweiRDS && slices.Contains([]string{"mysql", "postgresql", "sqlserver"}, db.Engine)
	default:
		return false
	}
}

func NormalizeDatabaseBackups(rows []DatabaseBackup) error {
	if rows == nil || len(rows) > MaxDatabaseBackups {
		return &Failure{Kind: InvalidResponse}
	}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, exists := seen[row.ID]; exists || !databaseAssetID.MatchString(row.ID) || row.StartedAt.IsZero() ||
			(row.EndedAt != nil && row.EndedAt.Before(row.StartedAt)) || (row.SizeBytes != nil && *row.SizeBytes < 0) ||
			!slices.Contains([]string{"full", "incremental", "fragment", "unknown"}, row.Kind) ||
			!slices.Contains([]string{"automatic", "manual", "unknown"}, row.Mode) ||
			!slices.Contains([]string{"ready", "pending", "failed", "deleting", "unknown"}, row.Status) {
			return &Failure{Kind: InvalidResponse}
		}
		seen[row.ID] = struct{}{}
	}
	slices.SortFunc(rows, func(a, b DatabaseBackup) int {
		return cmp.Or(b.StartedAt.Compare(a.StartedAt), cmp.Compare(a.ID, b.ID))
	})
	return nil
}
