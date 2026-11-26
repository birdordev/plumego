package inventory

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"with-cloud-manager/internal/provider"
)

type TagComplianceQuery struct {
	Query
	State string
}

type TagComplianceItem struct {
	Account     string        `json:"account"`
	Provider    string        `json:"provider"`
	Region      string        `json:"region"`
	Kind        provider.Kind `json:"kind"`
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	State       string        `json:"state"`
	Reason      string        `json:"reason,omitempty"`
	ObservedAt  *time.Time    `json:"tags_observed_at,omitempty"`
	MissingKeys []string      `json:"missing_keys,omitempty"`
}

type TagCompliance struct {
	PolicyRevision int64               `json:"policy_revision"`
	Items          []TagComplianceItem `json:"items"`
	Next           string              `json:"next,omitempty"`
}

func (s Store) TagCompliance(ctx context.Context, token string, q TagComplianceQuery) (TagCompliance, error) {
	scope := TagPolicyScope{Tenant: q.Tenant, Project: q.Project}
	if !scope.valid() || !q.Kind.Valid() || q.Search != "" || !validInventoryText(q.Account) || !validInventoryText(q.Region) ||
		(q.State != "" && q.State != "compliant" && q.State != "noncompliant" && q.State != "unknown") {
		return TagCompliance{}, ErrInvalidQuery
	}
	if _, err := queryPosition(Query{Tenant: q.Tenant, Project: q.Project, Account: q.Account,
		Region: q.Region, Kind: q.Kind, Limit: q.Limit}); err != nil {
		return TagCompliance{}, err
	}
	var result TagCompliance
	err := s.readProject(ctx, token, q.Query, func(tx *sql.Tx) error {
		policy, err := readTagPolicy(ctx, tx, scope, 0)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(policy.Rules, func(rule TagRule) bool { return rule.Kind == q.Kind })
		if i < 0 {
			return ErrTagPolicyNotFound
		}
		watermark := q.Query
		watermark.Search = q.State + "/" + strconv.FormatInt(policy.Revision, 10)
		p, err := queryPosition(watermark)
		if err != nil {
			return err
		}
		if !validInventoryText(p.Account) || !validInventoryText(p.Region) || !validInventoryText(p.ID) {
			return ErrInvalidQuery
		}
		result = TagCompliance{PolicyRevision: policy.Revision, Items: make([]TagComplianceItem, 0, q.Limit)}
		return listTagCompliance(ctx, tx, q, p, policy, policy.Rules[i], &result)
	})
	if err != nil {
		return TagCompliance{}, err
	}
	return result, nil
}

//go:embed tag_compliance.sql
var tagComplianceSelect string

func listTagCompliance(ctx context.Context, tx *sql.Tx, q TagComplianceQuery, p position, policy TagPolicy, rule TagRule, result *TagCompliance) error {
	keys, err := json.Marshal(rule.RequiredKeys)
	if err != nil {
		return err
	}
	// 每个盘点范围最多贡献一页候选。缺失键只在全局排序并截取分页后展开。
	rows, err := tx.QueryContext(ctx, tagComplianceSelect+inventoryOwnershipJoins+`
				WHERE o.scope_id=s.scope_id AND o.removed_at IS NULL
				  AND `+assetVisibility+`
				  AND ($6='' OR (s.account_id,s.region,o.external_id)>($6,$7,$8))
			) AS assessed
		) AS classified
		WHERE $11='' OR state=$11
		ORDER BY external_id LIMIT $12
	) AS candidate
	WHERE s.tenant_id=$1 AND s.kind=$5
	  AND ($3='' OR s.account_id=$3) AND ($4='' OR s.region=$4)
	  AND ($6='' OR (s.account_id,s.region)>=($6,$7))
	ORDER BY s.account_id,s.region,candidate.external_id LIMIT $12
	)
	SELECT account_id,provider,region,kind,external_id,name,tag_observed_at,reason,state,
		to_json(CASE WHEN state='noncompliant' THEN ARRAY(
			SELECT req.key FROM jsonb_array_elements_text($10::jsonb) WITH ORDINALITY AS req(key,ord)
			WHERE NOT req.key=ANY(page.tag_keys) ORDER BY req.ord
		) ELSE ARRAY[]::text[] END)
	FROM page ORDER BY account_id,region,external_id`, q.Tenant, q.Project, q.Account, q.Region, q.Kind,
		p.Account, p.Region, p.ID, policy.MaxAgeSeconds, string(keys), q.State, q.Limit+1)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a TagComplianceItem
		var missing []byte
		if err := rows.Scan(&a.Account, &a.Provider, &a.Region, &a.Kind, &a.ID, &a.Name, &a.ObservedAt, &a.Reason, &a.State, &missing); err != nil {
			return err
		}
		if len(result.Items) == q.Limit {
			last := result.Items[len(result.Items)-1]
			next, err := json.Marshal(position{Filter: p.Filter, Account: last.Account, Region: last.Region, Kind: last.Kind, ID: last.ID})
			if err != nil {
				return err
			}
			result.Next = base64.RawURLEncoding.EncodeToString(next)
			break
		}
		if err := json.Unmarshal(missing, &a.MissingKeys); err != nil {
			return err
		}
		result.Items = append(result.Items, a)
	}
	return rows.Err()
}
