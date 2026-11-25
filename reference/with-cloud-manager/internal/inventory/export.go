package inventory

import (
	"context"
	"database/sql"
	"errors"
)

const exportLimit = 2000

var ErrExportTooLarge = errors.New("asset export exceeds row limit")

func (s Store) Export(ctx context.Context, token string, q Query) ([]Asset, error) {
	if q.Cursor != "" {
		return nil, ErrInvalidQuery
	}
	start, err := queryPosition(q)
	if err != nil {
		return nil, err
	}
	q.Limit = exportLimit
	page := Assets{Items: make([]Asset, 0, q.Limit)}
	err = s.readProject(ctx, token, q, func(tx *sql.Tx) error {
		return listAssets(ctx, tx, q, start, &page)
	})
	if err != nil {
		return nil, err
	}
	if page.Next != "" {
		return nil, ErrExportTooLarge
	}
	return page.Items, nil
}
