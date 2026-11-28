package alerts

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/migrations"
)

func Migrate(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0028_project_alerts"); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0034_security_rule_outbox"); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0047_image_deletion_outbox"); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0060_load_balancer_rename_outbox"); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0073_dns_zone_outbox"); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, "0092_workload_scale_outbox")
}

type ProjectEvaluation func(context.Context, *sql.Tx, string, string) error

func (s Store) WithProjectEvaluation(evaluate ProjectEvaluation) Store {
	s.evaluate = evaluate
	return s
}

func (s Store) EvaluateNext(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var tenant, project string
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, migrations.Evaluate()).Scan(&tenant, &project)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil || s.evaluate == nil {
			return err
		}
		return s.evaluate(ctx, tx, tenant, project)
	})
	return err == nil && project != "", err
}

func (s Store) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		worked, err := s.EvaluateNext(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		if !worked {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
		}
	}
	return nil
}
