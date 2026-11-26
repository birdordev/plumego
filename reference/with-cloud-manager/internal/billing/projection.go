package billing

import (
	"context"
	"database/sql"
	"encoding/json"
)

const recordsetSQL = `SELECT ordinality::integer AS row_no,
	j->>'currency' AS currency, (j->>'billed')::numeric AS billed, (j->>'effective')::numeric AS effective,
	(j->>'list_cost')::numeric AS list_cost, NULLIF(j->>'resource_id','') AS resource_id,
	NULLIF(j->>'service','') AS service, NULLIF(j->>'region','') AS region,
	NULLIF(j->>'category','') AS category, NULLIF(j->>'tags','') AS tags
	FROM jsonb_array_elements($3::jsonb) WITH ORDINALITY AS source(j,ordinality)`

const totalProjectionSQL = `WITH amounts AS (
	SELECT currency,sum(billed) AS billed,
		CASE WHEN count(effective)=count(*) THEN sum(effective) END AS effective,
		CASE WHEN count(list_cost)=count(*) THEN sum(list_cost) END AS list_cost
	FROM cmp_billing_lines WHERE report_id=$1 GROUP BY currency
), expected AS (
	SELECT currency, value::numeric AS billed FROM jsonb_each_text(COALESCE($2::jsonb->'expected_billed','{}'::jsonb)) AS control(currency,value)
)
SELECT COALESCE(a.currency,e.currency),COALESCE(a.billed,0),a.effective,a.list_cost,e.billed
FROM amounts a FULL JOIN expected e USING (currency)`

func storePrepared(ctx context.Context, tx *sql.Tx, reportID string, prepared preparedReport) error {
	for index, part := range prepared.parts {
		number := index + 1
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_billing_parts(report_id,part_no,digest,raw,row_count)
			VALUES ($1,$2,$3,$4,$5)`, reportID, number, part.digest, part.data, len(part.lines)); err != nil {
			return err
		}
		if len(part.lines) == 0 {
			continue
		}
		projected, err := json.Marshal(part.lines)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_billing_lines
			(report_id,part_no,row_no,currency,billed,effective,list_cost,resource_id,service,region,category,tags)
			SELECT $1,$2,line.* FROM (`+recordsetSQL+`) line`, reportID, number, projected); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO cmp_billing_totals(report_id,currency,billed,effective,list_cost,expected_billed)
		SELECT $1,t.* FROM (`+totalProjectionSQL+`) t`, reportID, prepared.manifest)
	return err
}
