package inventory

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"time"
)

type Change struct {
	Account        string    `json:"account"`
	Region         string    `json:"region"`
	Kind           string    `json:"kind"`
	AssetID        string    `json:"asset_id"`
	Action         string    `json:"action"`
	PreviousName   *string   `json:"previous_name,omitempty"`
	PreviousStatus *string   `json:"previous_status,omitempty"`
	Name           string    `json:"name"`
	Status         string    `json:"status"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type Changes struct {
	Items []Change `json:"items"`
	Next  string   `json:"next,omitempty"`
}

type historyPosition struct {
	Filter string
	Time   time.Time
	ID     string
}

var eventID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s Store) History(ctx context.Context, token string, q Query) (Changes, error) {
	cursor := q.Cursor
	q.Cursor = ""
	key, err := queryPosition(q)
	if err != nil {
		return Changes{}, err
	}
	position := historyPosition{Filter: key.Filter}
	if cursor != "" {
		if len(cursor) > 1024 {
			return Changes{}, ErrInvalidQuery
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &position) != nil || position.Filter != key.Filter ||
			position.Time.IsZero() || !eventID.MatchString(position.ID) {
			return Changes{}, ErrInvalidQuery
		}
	}
	result := Changes{Items: make([]Change, 0, q.Limit)}
	err = s.readProject(ctx, token, q, func(tx *sql.Tx) error {
		return listHistory(ctx, tx, q, position, &result)
	})
	if err != nil {
		return Changes{}, err
	}
	return result, nil
}

func listHistory(ctx context.Context, tx *sql.Tx, q Query, cursor historyPosition, result *Changes) error {
	var stamp, event any
	if cursor.ID != "" {
		stamp, event = cursor.Time, cursor.ID
	}
	rows, err := tx.QueryContext(ctx, `SELECT h.event_id::text,s.account_id,s.region,s.kind,h.external_id,
			h.action,h.previous_name,h.previous_status,h.name,h.status,h.occurred_at
		FROM cmp_inventory_history AS h JOIN cmp_inventory_scopes AS s USING (scope_id)
		JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		LEFT JOIN cmp_inventory_objects AS o ON (o.scope_id,o.external_id)=(h.scope_id,h.external_id)`+inventoryOwnershipJoins+`
		WHERE `+assetVisibility+`
		AND ($3='' OR s.account_id=$3) AND ($4='' OR s.region=$4) AND ($5='' OR s.kind=$5)
		AND ($6='' OR strpos(lower(h.name),lower($6))>0 OR strpos(lower(h.external_id),lower($6))>0)
		AND ($7::timestamptz IS NULL OR (h.occurred_at,h.event_id)<($7,$8::uuid))
		ORDER BY h.occurred_at DESC,h.event_id DESC LIMIT $9`,
		q.Tenant, q.Project, q.Account, q.Region, q.Kind, q.Search, stamp, event, q.Limit+1)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	last := cursor
	for rows.Next() {
		if len(result.Items) == q.Limit {
			raw, err := json.Marshal(last)
			if err != nil {
				return err
			}
			result.Next = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		var item Change
		if err := rows.Scan(&last.ID, &item.Account, &item.Region, &item.Kind, &item.AssetID,
			&item.Action, &item.PreviousName, &item.PreviousStatus, &item.Name, &item.Status, &item.OccurredAt); err != nil {
			return err
		}
		last.Time = item.OccurredAt
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}
