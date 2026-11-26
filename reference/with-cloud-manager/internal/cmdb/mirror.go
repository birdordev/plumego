package cmdb

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"io"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
)

//go:embed 0001_mirror.sql
var mirrorSchema string

var (
	ErrStaleSnapshot = errors.New("CMDB snapshot was superseded")
	ErrNoReceipt     = errors.New("CMDB mirror has no published snapshot")
)

type MirrorKey struct {
	Source string
	Scope  Project
}

type Receipt struct {
	Project
	Summary
	Generation  int64     `json:"generation,string"`
	CapturedAt  time.Time `json:"captured_at"`
	PublishedAt time.Time `json:"published_at"`
	Upserted    int64     `json:"upserted"`
	Removed     int64     `json:"removed"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) Store { return Store{db: db} }

func Migrate(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "cmdb_0001_mirror", mirrorSchema)
}

func (s Store) reserve(ctx context.Context, key MirrorKey) (int64, error) {
	var generation int64
	err := s.db.QueryRowContext(ctx, `INSERT INTO cmp_cmdb_mirrors AS mirror (source_url,tenant_id,project_id,issued_generation)
		VALUES ($1,$2,$3,1) ON CONFLICT (source_url,tenant_id,project_id)
		DO UPDATE SET issued_generation=mirror.issued_generation+1 RETURNING issued_generation`,
		key.Source, key.Scope.Tenant, key.Scope.Project).Scan(&generation)
	return generation, err
}

func (s Store) apply(ctx context.Context, key MirrorKey, generation int64, input io.Reader) (Receipt, error) {
	var receipt Receipt
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		manifest, summary, err := stageSnapshot(ctx, tx, key.Scope, input)
		if err != nil {
			return err
		}
		receipt = Receipt{Project: key.Scope, Summary: summary, Generation: generation, CapturedAt: manifest.CapturedAt}
		return publishSnapshot(ctx, tx, key, &receipt)
	})
	if err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (s Store) Status(ctx context.Context, key MirrorKey) (Receipt, error) {
	receipt := Receipt{Project: key.Scope}
	err := s.db.QueryRowContext(ctx, `SELECT generation,captured_at,published_at,asset_count,upserted,removed,digest
		FROM cmp_cmdb_reconciliations WHERE (source_url,tenant_id,project_id)=($1,$2,$3)
		ORDER BY generation DESC LIMIT 1`, key.Source, key.Scope.Tenant, key.Scope.Project).Scan(
		&receipt.Generation, &receipt.CapturedAt, &receipt.PublishedAt, &receipt.Count, &receipt.Upserted, &receipt.Removed, &receipt.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNoReceipt
	}
	return receipt, err
}
