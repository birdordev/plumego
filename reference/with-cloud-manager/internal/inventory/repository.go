package inventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/provider"
)

//go:embed 0002_inventory.sql
var schema string

//go:embed 0005_inventory_history.sql
var historySchema string

//go:embed 0006_inventory_topology.sql
var topologySchema string

//go:embed 0015_confirmed_instance_deletions.sql
var confirmedDeletionsSchema string

//go:embed 0035_inventory_public_addresses.sql
var publicAddressesSchema string

//go:embed 0040_snapshot_inventory.sql
var snapshotInventorySchema string

//go:embed 0048_instance_image_reservations.sql
var instanceImageReservationsSchema string

//go:embed 0050_image_copy_reservations.sql
var imageCopyReservationsSchema string

//go:embed 0054_network_deletion_reservations.sql
var networkDeletionReservationsSchema string

//go:embed 0056_load_balancer_inventory.sql
var loadBalancerInventorySchema string

var ErrUnavailable = errors.New("inventory scope unavailable")
var ErrStale = errors.New("inventory observation is no longer current")

type Key struct {
	Tenant  string
	Account string
	Region  string
	Kind    provider.Kind
}

type Claim struct {
	id         int64
	fence      int64
	generation int64
	cursor     string
	schedule   ScheduleLease
	key        Key
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) Store {
	return Store{db: db}
}

//go:embed 0076_database_inventory.sql
var databaseInventorySchema string

//go:embed 0078_object_bucket_inventory.sql
var objectBucketInventorySchema string

//go:embed 0080_cache_inventory.sql
var cacheInventorySchema string

//go:embed 0082_kubernetes_inventory.sql
var kubernetesInventorySchema string

//go:embed 0134_inventory_completion.sql
var inventoryCompletionSchema string

const InventoryCompletionMigration = "0134_inventory_completion"

//go:embed 0156_volume_inventory_details.sql
var volumeInventorySchema string

const VolumeInventoryMigration = "0156_volume_inventory_details"

//go:embed 0160_instance_type_inventory.sql
var instanceTypeInventorySchema string

const InstanceTypeInventoryMigration = "0160_instance_type_inventory"

func Migrate(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0002_inventory", schema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0005_inventory_history", historySchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0006_inventory_topology", topologySchema); err != nil {
		return err
	}
	if err := MigrateOwnership(ctx, db); err != nil {
		return err
	}
	if err := migrateTagAssignment(ctx, db); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0015_confirmed_instance_deletions", confirmedDeletionsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0035_inventory_public_addresses", publicAddressesSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0040_snapshot_inventory", snapshotInventorySchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0048_instance_image_reservations", instanceImageReservationsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0050_image_copy_reservations", imageCopyReservationsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0054_network_deletion_reservations", networkDeletionReservationsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0056_load_balancer_inventory", loadBalancerInventorySchema); err != nil {
		return err
	}
	if err := migrateLoadBalancerConfiguration(ctx, db); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0067_dns_zone_records", dnsZoneRecordsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0069_load_balancer_change_reservations", loadBalancerChangeReservationsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0071_dns_zone_mutation", dnsZoneMutationSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0076_database_inventory", databaseInventorySchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0078_object_bucket_inventory", objectBucketInventorySchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0080_cache_inventory", cacheInventorySchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0082_kubernetes_inventory", kubernetesInventorySchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0084_cluster_node_pools", clusterNodePoolsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0086_cluster_workloads", clusterWorkloadsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0088_cluster_workload_checkpoints", workloadCheckpointSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0090_workload_change_reservations", workloadChangeReservationsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0097_tag_observations", tagObservationsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0103_database_rename_reservations", databaseRenameReservationsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0106_instance_retirement_reservations", instanceRetirementReservationsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0108_cache_rename_reservations", cacheRenameReservationsSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, InventoryCompletionMigration, inventoryCompletionSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, BucketVersioningMigration, bucketVersioningSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, BucketEncryptionMigration, bucketEncryptionSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, VolumeInventoryMigration, volumeInventorySchema); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, InstanceTypeInventoryMigration, instanceTypeInventorySchema)
}

func (s Store) Claim(ctx context.Context, key Key, lease time.Duration) (Claim, error) {
	return s.ClaimWithCredentialRevision(ctx, key, lease, 0)
}

func (s Store) ClaimWithCredentialRevision(ctx context.Context, key Key, lease time.Duration, revision int64) (Claim, error) {
	return claimScope(ctx, s.db, key, lease, revision)
}

type scopeQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func claimScope(ctx context.Context, db scopeQuerier, key Key, lease time.Duration, revision int64) (Claim, error) {
	if revision < 0 || lease < time.Millisecond || lease > 15*time.Minute || !key.Kind.Valid() ||
		(key.Kind == provider.DNSZone && key.Region != provider.DNSGlobalScope) ||
		(key.Kind == provider.ObjectBucket && key.Region != provider.ObjectBucketGlobalScope) {
		return Claim{}, ErrUnavailable
	}
	var claim Claim
	err := db.QueryRowContext(ctx, `
		WITH account AS (
			SELECT tenant_id,account_id,credential_revision FROM cmp_cloud_accounts
			WHERE tenant_id=$1 AND account_id=$2 AND mode IN ('read_only','write_enabled') AND ($6::bigint=0 OR credential_revision=$6) FOR SHARE
		)
		INSERT INTO cmp_inventory_scopes AS scope (tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT tenant_id,account_id,$3,$4,credential_revision,statement_timestamp()+$5*interval '1 millisecond'
		FROM account
		ON CONFLICT (tenant_id,account_id,region,kind) DO UPDATE SET
			fence=scope.fence+1, running=true, lease_until=EXCLUDED.lease_until,
			credential_revision=EXCLUDED.credential_revision,
			generation=CASE WHEN scope.running AND scope.credential_revision=EXCLUDED.credential_revision
				THEN scope.generation ELSE scope.generation+1 END,
			cursor=CASE WHEN scope.running AND scope.credential_revision=EXCLUDED.credential_revision
				THEN scope.cursor ELSE '' END,
			pages=CASE WHEN scope.running AND scope.credential_revision=EXCLUDED.credential_revision
				THEN scope.pages ELSE 0 END
		WHERE NOT scope.running OR scope.lease_until<=statement_timestamp()
			OR scope.credential_revision<>EXCLUDED.credential_revision
		RETURNING scope_id,fence,generation,cursor`,
		key.Tenant, key.Account, key.Region, key.Kind, lease.Milliseconds(), revision).
		Scan(&claim.id, &claim.fence, &claim.generation, &claim.cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return Claim{}, ErrUnavailable
	}
	return claim, err
}

const active = ` FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	WHERE s.scope_id=$1 AND s.fence=$2 AND s.generation=$3 AND s.running
	  AND s.cursor=$4 AND s.lease_until>clock_timestamp()
	  AND a.mode IN ('read_only','write_enabled') AND a.credential_revision=s.credential_revision`

func (s Store) scope(ctx context.Context, claim Claim) (provider.Scope, error) {
	if claim.schedule.ID != 0 {
		if err := checkScheduleLease(ctx, s.db, claim); err != nil {
			return provider.Scope{}, err
		}
	}
	var scope provider.Scope
	err := s.db.QueryRowContext(ctx, "SELECT a.provider,a.remote_identity,s.region,s.kind"+active,
		claim.id, claim.fence, claim.generation, claim.cursor).
		Scan(&scope.Provider, &scope.RemoteIdentity, &scope.Region, &scope.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return provider.Scope{}, ErrStale
	}
	return scope, err
}

func (s Store) advance(ctx context.Context, claim Claim, page provider.Page, restart bool) (Claim, bool, error) {
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		var scopeID int64
		var pages int
		var kind provider.Kind
		err := tx.QueryRowContext(ctx, "SELECT s.scope_id,s.pages,s.kind"+active+" FOR UPDATE OF s FOR SHARE OF a",
			claim.id, claim.fence, claim.generation, claim.cursor).Scan(&scopeID, &pages, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		if claim.schedule.ID != 0 {
			if err := checkScheduleLease(ctx, tx, claim); err != nil {
				return err
			}
		}
		if claim.cursor == "" {
			if _, err := tx.ExecContext(ctx, "DELETE FROM cmp_inventory_cursors WHERE scope_id=$1", scopeID); err != nil {
				return err
			}
		}
		if page.Next != "" {
			if pages >= 9999 {
				return &provider.Failure{Kind: provider.InvalidResponse}
			}
			hash := sha256.Sum256([]byte(page.Next))
			result, err := tx.ExecContext(ctx, `INSERT INTO cmp_inventory_cursors(scope_id,fingerprint)
			VALUES ($1,$2) ON CONFLICT DO NOTHING`, scopeID, hash[:])
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count == 0 {
				return &provider.Failure{Kind: provider.InvalidResponse}
			}
		}
		if len(page.Objects) != 0 {
			if err := upsert(ctx, tx, scopeID, claim.generation, kind, page.Objects); err != nil {
				return err
			}
		}
		switch {
		case restart:
			_, err = tx.ExecContext(ctx, "UPDATE cmp_inventory_scopes SET running=false,cursor='' WHERE scope_id=$1", scopeID)
		case page.Next != "":
			_, err = tx.ExecContext(ctx, "UPDATE cmp_inventory_scopes SET cursor=$2,pages=pages+1 WHERE scope_id=$1", scopeID, page.Next)
		default:
			err = complete(ctx, tx, scopeID, claim.generation)
		}
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Claim{}, false, err
	}
	claim.cursor = page.Next
	return claim, page.Next == "", nil
}

func upsert(ctx context.Context, tx *sql.Tx, id, generation int64, kind provider.Kind, objects []provider.Object) error {
	data, err := json.Marshal(objects)
	if err != nil {
		return err
	}
	if kind == provider.SecurityGroupRule {
		if err := lockSecurityRuleParents(ctx, tx, id, string(data)); err != nil {
			return err
		}
	}
	if kind == provider.Snapshot {
		if err := verifySnapshotOrigins(ctx, tx, id, string(data)); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cmp_inventory_objects AS existing
		(scope_id,external_id,name,status,seen_generation,links,public_addresses,snapshot,load_balancer,database_instance,object_bucket,cache_instance,kubernetes_cluster,volume_details,instance_type,tag_keys,tag_observed_at,tag_credential_revision)
		SELECT $1, "ID", "Name", "Status", $2, COALESCE(links,'[]'::jsonb), COALESCE(public_addresses,'[]'::jsonb), snapshot, load_balancer, database_instance, object_bucket, cache_instance, kubernetes_cluster, volume, COALESCE(instance_type,''),
			CASE WHEN jsonb_typeof(tags)='object' THEN ARRAY(
				SELECT tag.key FROM jsonb_each_text(tags) AS tag WHERE tag.value !~ '^[[:space:]]*$' ORDER BY tag.key
			) END,
			CASE WHEN jsonb_typeof(tags)='object' THEN statement_timestamp() END,
			CASE WHEN jsonb_typeof(tags)='object' THEN (SELECT credential_revision FROM cmp_inventory_scopes WHERE scope_id=$1) END
		FROM (
			SELECT DISTINCT ON ("ID") "ID", "Name", "Status", links, public_addresses, snapshot, load_balancer, database_instance, object_bucket, cache_instance, kubernetes_cluster, volume, instance_type, tags
			FROM ROWS FROM(jsonb_to_recordset($3::jsonb) AS ("ID" text, "Name" text, "Status" text, links jsonb, public_addresses jsonb, snapshot jsonb, load_balancer jsonb, database_instance jsonb, object_bucket jsonb, cache_instance jsonb, kubernetes_cluster jsonb, volume jsonb, instance_type text, tags jsonb))
				WITH ORDINALITY AS records
			ORDER BY "ID", ordinality DESC
		) AS deduplicated
		WHERE $4<>'security-group-rule' OR NOT EXISTS (
			SELECT 1 FROM cmp_inventory_scopes AS rule_scope
			JOIN cmp_inventory_scopes AS group_scope USING (tenant_id,account_id,region)
			JOIN cmp_inventory_objects AS parent ON parent.scope_id=group_scope.scope_id
			WHERE rule_scope.scope_id=$1 AND group_scope.kind='security-group'
			  AND parent.external_id=deduplicated.links->0->>'id' AND parent.deletion_confirmed_at IS NOT NULL
		)
		ON CONFLICT (scope_id,external_id) DO UPDATE SET
			name=EXCLUDED.name,status=EXCLUDED.status,seen_generation=EXCLUDED.seen_generation,
			observed_at=EXCLUDED.observed_at,links=EXCLUDED.links ||
				CASE WHEN $4='volume' AND NOT EXISTS (
					SELECT 1 FROM jsonb_array_elements(EXCLUDED.links) AS link WHERE link->>'relation'='restored_from'
				) THEN COALESCE((SELECT jsonb_agg(link) FROM jsonb_array_elements(existing.links) AS link
					WHERE link->>'kind'='snapshot' AND link->>'relation'='restored_from'),'[]'::jsonb)
				ELSE '[]'::jsonb END ||
				CASE WHEN $4='image' AND NOT EXISTS (
					SELECT 1 FROM jsonb_array_elements(EXCLUDED.links) AS link WHERE link->>'relation'='created_from'
				) THEN COALESCE((SELECT jsonb_agg(link) FROM jsonb_array_elements(existing.links) AS link
					WHERE link->>'kind'='instance' AND link->>'relation'='created_from'),'[]'::jsonb)
				ELSE '[]'::jsonb END,public_addresses=EXCLUDED.public_addresses,
			missing_generation=NULL,removed_at=NULL,snapshot=EXCLUDED.snapshot,load_balancer=EXCLUDED.load_balancer,database_instance=EXCLUDED.database_instance,object_bucket=EXCLUDED.object_bucket,cache_instance=EXCLUDED.cache_instance,kubernetes_cluster=EXCLUDED.kubernetes_cluster,
			volume_details=EXCLUDED.volume_details,instance_type=EXCLUDED.instance_type,tag_keys=EXCLUDED.tag_keys,tag_observed_at=EXCLUDED.tag_observed_at,tag_credential_revision=EXCLUDED.tag_credential_revision
	WHERE existing.deletion_confirmed_at IS NULL`, id, generation, string(data), kind)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(objects, func(o provider.Object) bool { return o.Tags != nil }) {
		return nil
	}
	return assignTagProjects(ctx, tx, id, string(data))
}

// 与父组删除使用相同的锁顺序，等待删除提交后再判断迟到页能否写入。
func lockSecurityRuleParents(ctx context.Context, tx *sql.Tx, scopeID int64, data string) error {
	_, err := tx.ExecContext(ctx, `SELECT parent.external_id FROM cmp_inventory_scopes AS rule_scope
		JOIN cmp_inventory_scopes AS group_scope USING (tenant_id,account_id,region)
		JOIN cmp_inventory_objects AS parent ON parent.scope_id=group_scope.scope_id
		WHERE rule_scope.scope_id=$1 AND group_scope.kind='security-group'
		  AND parent.external_id IN (SELECT links->0->>'id' FROM jsonb_to_recordset($2::jsonb) AS incoming(links jsonb))
		ORDER BY group_scope.scope_id,parent.external_id
		FOR SHARE OF parent`, scopeID, data)
	return err
}

func verifySnapshotOrigins(ctx context.Context, tx *sql.Tx, scopeID int64, data string) error {
	var changed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM jsonb_to_recordset($2::jsonb) AS incoming("ID" text,links jsonb)
		JOIN cmp_inventory_objects AS o ON (o.scope_id,o.external_id)=($1,incoming."ID")
		WHERE o.links->0->>'relation'='snapshot_of'
		  AND o.links->0 IS DISTINCT FROM incoming.links->0)`, scopeID, data).Scan(&changed)
	if err != nil {
		return err
	}
	if changed {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func complete(ctx context.Context, tx *sql.Tx, id, generation int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE cmp_inventory_objects SET missing_generation=$2
		WHERE scope_id=$1 AND seen_generation<$2 AND removed_at IS NULL`, id, generation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM cmp_inventory_cursors WHERE scope_id=$1", id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE cmp_inventory_scopes SET
		running=false,cursor='',completed_at=statement_timestamp(),completed_credential_revision=credential_revision
		WHERE scope_id=$1`, id)
	return err
}

func (s Store) Yield(ctx context.Context, claim Claim) error {
	_, err := s.db.ExecContext(ctx, `UPDATE cmp_inventory_scopes SET lease_until=clock_timestamp()
		WHERE scope_id=$1 AND fence=$2 AND running`, claim.id, claim.fence)
	return err
}
