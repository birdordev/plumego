package inventory

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed tag_policy_alerts.sql
var tagPolicyAlertChanges string

var tagPolicyAlertAssessment = `WITH policy AS MATERIALIZED (
	SELECT revision,max_age_seconds,rules,alert_on_noncompliance FROM cmp_tag_policies
	WHERE (tenant_id,project_id)=($1,$2) ORDER BY revision DESC LIMIT 1
), rules AS MATERIALIZED (
	SELECT p.revision,p.max_age_seconds,r.kind,ARRAY(SELECT jsonb_array_elements_text(r.required_keys)) AS keys
	FROM policy AS p CROSS JOIN LATERAL jsonb_to_recordset(p.rules) AS r(kind text,required_keys jsonb)
	WHERE p.alert_on_noncompliance
), assessed AS (
	SELECT r.revision,r.kind,o.tag_keys @> r.keys AS compliant,
		(` + tagObservationReason("r.max_age_seconds") + `)='' AND (` + ownershipSource + `)<>'pending' AS known
	FROM rules AS r
	JOIN cmp_inventory_scopes AS s ON s.tenant_id=$1 AND s.kind=r.kind
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	JOIN cmp_inventory_objects AS o USING (scope_id)` + inventoryOwnershipJoins + `
	WHERE o.removed_at IS NULL AND (` + projectedOwner + `)=$2 AND (` + assetVisibility + `)
)`

func EvaluateTagPolicyAlerts(ctx context.Context, tx *sql.Tx, tenant, project string) error {
	_, err := tx.ExecContext(ctx, tagPolicyAlertAssessment+tagPolicyAlertChanges, tenant, project)
	return err
}
