package alerts

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
)

//go:embed 0028_project_alerts.sql
var schema string

//go:embed 0034_security_rule_outbox.sql
var securityRuleOutboxSchema string

//go:embed 0047_image_deletion_outbox.sql
var imageDeletionOutboxSchema string

//go:embed 0060_load_balancer_rename_outbox.sql
var loadBalancerRenameOutboxSchema string

//go:embed 0073_dns_zone_outbox.sql
var dnsZoneOutboxSchema string

//go:embed 0092_workload_scale_outbox.sql
var workloadScaleOutboxSchema string

//go:embed evaluate.sql
var evaluate string

func Migrate(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0028_project_alerts", schema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0034_security_rule_outbox", securityRuleOutboxSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0047_image_deletion_outbox", imageDeletionOutboxSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0060_load_balancer_rename_outbox", loadBalancerRenameOutboxSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0073_dns_zone_outbox", dnsZoneOutboxSchema); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, "0092_workload_scale_outbox", workloadScaleOutboxSchema)
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
		err := tx.QueryRowContext(ctx, evaluate).Scan(&tenant, &project)
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
