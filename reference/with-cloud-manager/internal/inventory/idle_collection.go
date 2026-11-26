package inventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"time"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

//go:embed 0100_instance_idle.sql
var idleInstanceSchema string

const idleCPUThreshold = 5
const idleNetworkThreshold = 64 << 10

type idleTarget struct {
	scopeID, credential, ownership, observed, generation int64
	provider, remote, project, status, reference         string
}

func MigrateIdleAnalysis(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0100_instance_idle", idleInstanceSchema)
}

func (s Store) PrepareIdleAnalysis(ctx context.Context, key Key, id, reference string, revision int64,
	collect func(context.Context, provider.Scope, string, provider.MetricWindow) (provider.InstanceUtilization, error),
) (func(context.Context, *sql.Tx) error, error) {
	if key.Kind != provider.Instance || !validInventoryText(id) || len(id) == 0 || len(id) > provider.MaxObjectIDSize || revision < 1 || reference == "" {
		return nil, ErrInvalidQuery
	}
	target, err := readIdleTarget(ctx, s.db, key, id)
	if errors.Is(err, sql.ErrNoRows) {
		// 暂无可用盘点证据。保留计划，等待下一轮完整盘点。
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if target.credential != revision || target.reference != reference {
		return nil, ErrStale
	}
	status := strings.ToLower(target.status)
	if status != "running" && status != "active" {
		return nil, nil
	}
	until := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	window := provider.MetricWindow{From: until.Add(-provider.IdleWindow), Until: until}
	scope := provider.Scope{Provider: target.provider, RemoteIdentity: target.remote, Region: key.Region, Kind: provider.Instance}
	usage, err := collect(ctx, scope, id, window)
	if err != nil {
		return nil, err
	}
	if !usage.Valid(window) {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	evidence := summarizeIdleUsage(window, usage)
	return func(ctx context.Context, tx *sql.Tx) error {
		var scopeID int64
		err := tx.QueryRowContext(ctx, `SELECT s.scope_id FROM cmp_inventory_scopes AS s
			JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id) JOIN cmp_inventory_objects AS o USING (scope_id)
			WHERE (s.tenant_id,s.account_id,s.region,s.kind,o.external_id)=($1,$2,$3,'instance',$4)
			FOR SHARE OF s,a,o`, key.Tenant, key.Account, key.Region, id).Scan(&scopeID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		current, err := readIdleTarget(ctx, tx, key, id)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && current != target) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		return writeIdleEvidence(ctx, tx, key.Tenant, target, id, evidence)
	}, nil
}

func readIdleTarget(ctx context.Context, db identity.Querier, key Key, id string) (idleTarget, error) {
	var target idleTarget
	err := db.QueryRowContext(ctx, `SELECT s.scope_id,a.provider,a.remote_identity,a.credential_ref,a.credential_revision,
		`+projectedOwner+`,`+projectedRevision+`,o.status,(extract(epoch FROM o.observed_at)*1000000)::bigint,o.seen_generation
		FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		JOIN cmp_inventory_objects AS o USING (scope_id)`+directInventoryOwnershipJoins+`
		WHERE (s.tenant_id,s.account_id,s.region,s.kind,o.external_id)=($1,$2,$3,'instance',$4)
		  AND o.removed_at IS NULL AND o.missing_generation IS NULL AND a.credential_revision=s.credential_revision
		  AND o.seen_generation=s.generation AND a.mode IN ('read_only','write_enabled')
		  AND o.observed_at>statement_timestamp()-interval '30 hours' AND (`+ownershipSource+`)<>'pending'`,
		key.Tenant, key.Account, key.Region, id).Scan(&target.scopeID, &target.provider, &target.remote, &target.reference,
		&target.credential, &target.project, &target.ownership, &target.status, &target.observed, &target.generation)
	return target, err
}

type idleEvidence struct {
	window           provider.MetricWindow
	period           int64
	cpu, network     int
	cpuPeak, netPeak *float64
	fingerprint      []byte
}

func summarizeIdleUsage(window provider.MetricWindow, series provider.InstanceUtilization) idleEvidence {
	h := sha256.New()
	var word [9]byte
	add := func(value uint64, flag byte) {
		word[0] = flag
		binary.BigEndian.PutUint64(word[1:], value)
		_, _ = h.Write(word[:])
	}
	add(uint64(window.From.Unix()), 1)
	add(uint64(window.Until.Unix()), 2)
	add(uint64(series.Period/time.Second), 3)
	summarize := func(samples []provider.MetricValue) (int, *float64) {
		var count int
		var peak float64
		for _, sample := range samples {
			if sample.Known {
				add(math.Float64bits(sample.Value), 1)
				count++
				peak = max(peak, sample.Value)
			} else {
				add(0, 0)
			}
		}
		if count == 0 {
			return 0, nil
		}
		return count, &peak
	}
	cpu, cpuPeak := summarize(series.CPU)
	network, netPeak := summarize(series.Network)
	return idleEvidence{window: window, period: int64(series.Period / time.Second), cpu: cpu, network: network,
		cpuPeak: cpuPeak, netPeak: netPeak, fingerprint: h.Sum(nil)}
}

func writeIdleEvidence(ctx context.Context, tx *sql.Tx, tenant string, target idleTarget, id string, evidence idleEvidence) error {
	res, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_idle_evidence AS current (
		scope_id,external_id,tenant_id,project_id,ownership_revision,credential_revision,
		window_start,window_end,period_seconds,cpu_samples,network_samples,cpu_peak,network_peak,fingerprint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (scope_id,external_id) DO UPDATE SET
			project_id=EXCLUDED.project_id,ownership_revision=EXCLUDED.ownership_revision,credential_revision=EXCLUDED.credential_revision,
			window_start=EXCLUDED.window_start,window_end=EXCLUDED.window_end,period_seconds=EXCLUDED.period_seconds,
			cpu_samples=EXCLUDED.cpu_samples,network_samples=EXCLUDED.network_samples,
			cpu_peak=EXCLUDED.cpu_peak,network_peak=EXCLUDED.network_peak,fingerprint=EXCLUDED.fingerprint
		WHERE current.window_end<=EXCLUDED.window_end`, target.scopeID, id, tenant, target.project, target.ownership, target.credential,
		evidence.window.From, evidence.window.Until, evidence.period, evidence.cpu, evidence.network, evidence.cpuPeak, evidence.netPeak, evidence.fingerprint)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err == nil && rows != 1 {
		return ErrStale
	}
	return err
}
