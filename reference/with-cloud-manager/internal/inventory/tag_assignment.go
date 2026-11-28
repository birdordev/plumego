package inventory

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/dbschema"
)

func migrateTagAssignment(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0009_tag_assignment")
}

const eligibleRuleProject = `rule.state='mapped' AND (rule.project_id=a.project_id OR EXISTS (
		SELECT 1 FROM cmp_cloud_account_grants AS permitted
		WHERE (permitted.tenant_id,permitted.account_id,permitted.project_id)=(s.tenant_id,s.account_id,rule.project_id)))`

const projectedOwner = `COALESCE(own.project_id,CASE WHEN ` + eligibleRuleProject + ` THEN rule.project_id END,a.project_id)`

const projectedRevision = `COALESCE(own.revision,COALESCE(rule.revision,0)+a.credential_revision)`

const ownershipSource = `CASE
	WHEN own.project_id IS NOT NULL THEN 'manual'
	WHEN ` + eligibleRuleProject + ` THEN 'tag_rule'
	WHEN rule.state IN ('mapped','conflict') THEN 'pending'
	ELSE 'account' END`

func assignTagProjects(ctx context.Context, tx *sql.Tx, id int64, data string) error {
	_, err := tx.ExecContext(ctx, `WITH incoming AS (
		SELECT DISTINCT ON ("ID") "ID",tags
		FROM ROWS FROM(jsonb_to_recordset($2::jsonb) AS ("ID" text,tags jsonb))
		WITH ORDINALITY AS records ORDER BY "ID",ordinality DESC
	), matched AS (
		SELECT s.tenant_id,s.scope_id,i."ID" AS external_id,a.credential_revision,
			count(DISTINCT r.project_id) AS matches,min(r.project_id) AS project_id
		FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		CROSS JOIN incoming AS i
		JOIN cmp_inventory_objects AS o ON (o.scope_id,o.external_id)=(s.scope_id,i."ID") AND o.deletion_confirmed_at IS NULL
		LEFT JOIN LATERAL jsonb_each_text(CASE WHEN jsonb_typeof(i.tags)='object' THEN i.tags ELSE '{}'::jsonb END) AS tag ON true
		LEFT JOIN cmp_asset_tag_rules AS r ON (r.tenant_id,r.account_id,r.tag_key,r.tag_value)=(s.tenant_id,s.account_id,tag.key,tag.value)
		LEFT JOIN cmp_asset_ownership AS own ON (own.scope_id,own.external_id)=(s.scope_id,i."ID")
		WHERE s.scope_id=$1 AND jsonb_typeof(i.tags)='object' AND own.project_id IS NULL
		GROUP BY s.tenant_id,s.scope_id,i."ID",a.credential_revision
	)
	INSERT INTO cmp_asset_tag_assignments AS rule (tenant_id,scope_id,external_id,state,project_id,revision)
	SELECT m.tenant_id,m.scope_id,m.external_id,
		CASE m.matches WHEN 0 THEN 'inherited' WHEN 1 THEN 'mapped' ELSE 'conflict' END,
		CASE WHEN m.matches=1 THEN m.project_id END,
		GREATEST(m.credential_revision,COALESCE(prev.revision,1))+1
	FROM matched AS m LEFT JOIN cmp_asset_tag_assignments AS prev USING (scope_id,external_id)
	WHERE m.matches<>0 OR prev.scope_id IS NOT NULL
	ON CONFLICT (scope_id,external_id) DO UPDATE
		SET state=EXCLUDED.state,project_id=EXCLUDED.project_id,revision=EXCLUDED.revision
		WHERE (rule.state,rule.project_id) IS DISTINCT FROM (EXCLUDED.state,EXCLUDED.project_id)`, id, data)
	return err
}
