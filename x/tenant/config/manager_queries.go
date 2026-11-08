package config

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	tenant "github.com/birdordev/plumego/x/tenant/core"
)

const tenantSelectColumns = `
	id, quota_limits, allowed_models, allowed_tools,
	allowed_methods, allowed_paths, metadata, updated_at
`

const getTenantConfigQuery = `
	SELECT ` + tenantSelectColumns + `
	FROM tenants
	WHERE id = ?
`

const listTenantConfigsQuery = `
	SELECT ` + tenantSelectColumns + `
	FROM tenants
	ORDER BY id
	LIMIT ? OFFSET ?
`

const upsertTenantConfigQuery = `
	INSERT INTO tenants (
		id, quota_limits, allowed_models, allowed_tools,
		allowed_methods, allowed_paths, metadata, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (id)
	DO UPDATE SET
		quota_limits    = excluded.quota_limits,
		allowed_models  = excluded.allowed_models,
		allowed_tools   = excluded.allowed_tools,
		allowed_methods = excluded.allowed_methods,
		allowed_paths   = excluded.allowed_paths,
		metadata        = excluded.metadata,
		updated_at      = excluded.updated_at
`

type tenantConfigScanner interface {
	Scan(dest ...any) error
}

type tenantConfigRow struct {
	cfg                tenant.Config
	quotaLimitsJSON    sql.NullString
	allowedModelsJSON  sql.NullString
	allowedToolsJSON   sql.NullString
	allowedMethodsJSON sql.NullString
	allowedPathsJSON   sql.NullString
	metadataJSON       sql.NullString
	updatedAt          time.Time
}

func scanTenantConfigRow(scanner tenantConfigScanner) (tenantConfigRow, error) {
	var row tenantConfigRow
	err := scanner.Scan(
		&row.cfg.TenantID,
		&row.quotaLimitsJSON,
		&row.allowedModelsJSON,
		&row.allowedToolsJSON,
		&row.allowedMethodsJSON,
		&row.allowedPathsJSON,
		&row.metadataJSON,
		&row.updatedAt,
	)
	return row, err
}

func (r tenantConfigRow) config() (tenant.Config, error) {
	cfg := r.cfg
	if err := parseTenantConfigJSON(&cfg, r.quotaLimitsJSON, r.allowedModelsJSON, r.allowedToolsJSON, r.allowedMethodsJSON, r.allowedPathsJSON, r.metadataJSON); err != nil {
		return tenant.Config{}, err
	}
	cfg.UpdatedAt = r.updatedAt
	return cfg, nil
}

// parseTenantConfigJSON parses JSON fields from database NullString values into a tenant.Config.
func parseTenantConfigJSON(cfg *tenant.Config, quotaLimitsJSON, allowedModelsJSON, allowedToolsJSON, allowedMethodsJSON, allowedPathsJSON, metadataJSON sql.NullString) error {
	if quotaLimitsJSON.Valid && quotaLimitsJSON.String != "" {
		if err := json.Unmarshal([]byte(quotaLimitsJSON.String), &cfg.Quota.Limits); err != nil {
			return fmt.Errorf("parsing quota_limits: %w", err)
		}
	}
	if allowedModelsJSON.Valid {
		if err := json.Unmarshal([]byte(allowedModelsJSON.String), &cfg.Policy.AllowedModels); err != nil {
			return fmt.Errorf("parsing allowed_models: %w", err)
		}
	}
	if allowedToolsJSON.Valid {
		if err := json.Unmarshal([]byte(allowedToolsJSON.String), &cfg.Policy.AllowedTools); err != nil {
			return fmt.Errorf("parsing allowed_tools: %w", err)
		}
	}
	if allowedMethodsJSON.Valid && allowedMethodsJSON.String != "" {
		if err := json.Unmarshal([]byte(allowedMethodsJSON.String), &cfg.Policy.AllowedMethods); err != nil {
			return fmt.Errorf("parsing allowed_methods: %w", err)
		}
	}
	if allowedPathsJSON.Valid && allowedPathsJSON.String != "" {
		if err := json.Unmarshal([]byte(allowedPathsJSON.String), &cfg.Policy.AllowedPaths); err != nil {
			return fmt.Errorf("parsing allowed_paths: %w", err)
		}
	}
	if metadataJSON.Valid {
		if err := json.Unmarshal([]byte(metadataJSON.String), &cfg.Metadata); err != nil {
			return fmt.Errorf("parsing metadata: %w", err)
		}
	}
	return nil
}
