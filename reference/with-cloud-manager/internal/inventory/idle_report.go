package inventory

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

type IdleQuery struct {
	Query
	State string
}

type IdleEvidenceView struct {
	From           time.Time `json:"from"`
	Until          time.Time `json:"until"`
	PeriodSeconds  int       `json:"period_seconds"`
	Expected       int       `json:"expected_samples"`
	CPU            int       `json:"cpu_samples"`
	Network        int       `json:"network_samples"`
	CPUPeak        *float64  `json:"cpu_peak_percent,omitempty"`
	NetworkPeak    *float64  `json:"network_peak_bytes_per_second,omitempty"`
	EvidenceSHA256 string    `json:"evidence_sha256"`
}

type IdleInstance struct {
	Account  string            `json:"account"`
	Region   string            `json:"region"`
	Provider string            `json:"provider"`
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	State    string            `json:"state"`
	Reason   string            `json:"reason,omitempty"`
	Evidence *IdleEvidenceView `json:"evidence,omitempty"`
}

type IdleInstances struct {
	Items                       []IdleInstance `json:"items"`
	Next                        string         `json:"next,omitempty"`
	CPUThresholdPercent         int            `json:"cpu_threshold_percent"`
	NetworkThresholdBytesPerSec int            `json:"network_threshold_bytes_per_second"`
}

func (s Store) IdleInstances(ctx context.Context, token string, q IdleQuery) (IdleInstances, error) {
	if q.Search != "" || !validInventoryText(q.Account) || !validInventoryText(q.Region) ||
		(q.State != "" && q.State != "candidate" && q.State != "active" && q.State != "insufficient" && q.State != "not_running") {
		return IdleInstances{}, ErrInvalidQuery
	}
	q.Kind = provider.Instance
	watermark := q.Query
	watermark.Search = "idle/v1/" + q.State
	p, err := queryPosition(watermark)
	if err != nil || !validInventoryText(p.Account) || !validInventoryText(p.Region) || !validInventoryText(p.ID) {
		return IdleInstances{}, ErrInvalidQuery
	}
	result := IdleInstances{Items: make([]IdleInstance, 0, q.Limit),
		CPUThresholdPercent: idleCPUThreshold, NetworkThresholdBytesPerSec: idleNetworkThreshold}
	err = s.readProject(ctx, token, q.Query, func(tx *sql.Tx) error {
		return listIdleInstances(ctx, tx, q, p, &result)
	})
	if err != nil {
		return IdleInstances{}, err
	}
	return result, nil
}

//go:embed idle_report.sql
var idleReportSelect string

var idleReportFacts = strings.NewReplacer(
	"/* PROJECT_OWNER */", projectedOwner, "/* OWNERSHIP_VERSION */", projectedRevision,
).Replace(idleReportSelect)

const idleScopeFilter = `s.tenant_id=$1 AND s.kind='instance' AND a.provider IN ('tencent-cvm','alibaba-ecs','huawei-ecs')
	AND ($3='' OR s.account_id=$3) AND ($4='' OR s.region=$4)
	AND ($5='' OR (s.account_id,s.region)>=($5,$6))`

const idleInstanceFilter = `o.removed_at IS NULL AND ` + directAssetVisibility + `
	AND ($5='' OR (s.account_id,s.region,o.external_id)>($5,$6,$7))`

const idleEvidenceJoin = directInventoryOwnershipJoins + `
	LEFT JOIN cmp_instance_idle_evidence AS e ON (e.scope_id,e.external_id)=(o.scope_id,o.external_id)`

const idleReportResult = `
	SELECT account_id,region,provider,external_id,name,state,reason,
		CASE WHEN reason IN ('','incomplete_metrics') THEN window_start END,
		CASE WHEN reason IN ('','incomplete_metrics') THEN window_end END,
		CASE WHEN reason IN ('','incomplete_metrics') THEN period_seconds END,
		CASE WHEN reason IN ('','incomplete_metrics') THEN cpu_samples END,
		CASE WHEN reason IN ('','incomplete_metrics') THEN network_samples END,
		CASE WHEN reason IN ('','incomplete_metrics') THEN cpu_peak END,
		CASE WHEN reason IN ('','incomplete_metrics') THEN network_peak END,
		CASE WHEN reason IN ('','incomplete_metrics') THEN encode(fingerprint,'hex') END
	FROM page ORDER BY account_id,region,external_id`

var idleScopedReport = `WITH page AS (
	SELECT s.account_id,s.region,a.provider,candidate.*
	FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	CROSS JOIN LATERAL (
		SELECT classified.* FROM (
			SELECT facts.*, CASE
				WHEN reason='instance_not_running' THEN 'not_running'
				WHEN reason<>'' THEN 'insufficient'
				WHEN cpu_peak>$10 OR network_peak>$11 THEN 'active'
				ELSE 'candidate'
			END AS state
			FROM (` + idleReportFacts + `
				FROM cmp_inventory_objects AS o` + idleEvidenceJoin + `
				WHERE o.scope_id=s.scope_id AND ` + idleInstanceFilter + `
			) AS facts
		) AS classified
		WHERE $8='' OR state=$8
		ORDER BY external_id LIMIT $9
	) AS candidate
	WHERE ` + idleScopeFilter + `
	ORDER BY s.account_id,s.region,candidate.external_id LIMIT $9
)` + idleReportResult

// 证据不足通常是稀疏集合。采用集合连接查找候选，避免对每个范围发起一轮索引探测。
var idleInsufficientReport = `WITH scopes AS MATERIALIZED (
	SELECT s.scope_id,s.tenant_id,s.account_id,s.region,s.kind,s.credential_revision,s.generation,
		a.provider,a.mode,a.project_id,a.credential_revision AS account_revision
	FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	WHERE ` + idleScopeFilter + `
), page AS (
	SELECT s.account_id,s.region,s.provider,facts.*,'insufficient' AS state
	FROM scopes AS s
	CROSS JOIN LATERAL (SELECT s.mode,s.project_id,s.account_revision AS credential_revision) AS a
	JOIN cmp_inventory_objects AS o USING (scope_id)` + idleEvidenceJoin + `
	CROSS JOIN LATERAL (` + idleReportFacts + `) AS facts
	WHERE ` + idleInstanceFilter + `
		AND $8='insufficient' AND facts.reason NOT IN ('','instance_not_running')
	ORDER BY s.account_id,s.region,o.external_id LIMIT $9
)` + idleReportResult

func listIdleInstances(ctx context.Context, tx *sql.Tx, q IdleQuery, p position, result *IdleInstances) error {
	query := idleScopedReport
	if q.State == "insufficient" {
		query = idleInsufficientReport
	}
	rows, err := tx.QueryContext(ctx, query,
		q.Tenant, q.Project, q.Account, q.Region, p.Account, p.Region, p.ID, q.State, q.Limit+1,
		idleCPUThreshold, idleNetworkThreshold)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var row IdleInstance
		var start, end *time.Time
		var period, cpu, network *int
		var cpuPeak, netPeak *float64
		var fingerprint *string
		if err := rows.Scan(&row.Account, &row.Region, &row.Provider, &row.ID, &row.Name, &row.State, &row.Reason,
			&start, &end, &period, &cpu, &network, &cpuPeak, &netPeak, &fingerprint); err != nil {
			return err
		}
		if len(result.Items) == q.Limit {
			last := result.Items[len(result.Items)-1]
			next, err := json.Marshal(position{Filter: p.Filter, Account: last.Account, Region: last.Region, Kind: provider.Instance, ID: last.ID})
			if err != nil {
				return err
			}
			result.Next = base64.RawURLEncoding.EncodeToString(next)
			break
		}
		if start != nil && end != nil && period != nil && *period > 0 && cpu != nil && network != nil && fingerprint != nil {
			row.Evidence = &IdleEvidenceView{From: *start, Until: *end, PeriodSeconds: *period,
				Expected: int(provider.IdleWindow/time.Second) / *period, CPU: *cpu, Network: *network,
				CPUPeak: cpuPeak, NetworkPeak: netPeak, EvidenceSHA256: *fingerprint}
		}
		result.Items = append(result.Items, row)
	}
	return rows.Err()
}
