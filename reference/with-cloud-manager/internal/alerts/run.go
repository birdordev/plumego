package alerts

import (
	"context"
	"database/sql"
	_ "embed"
	"time"

	"with-cloud-manager/internal/dbschema"
)

//go:embed 0028_project_alerts.sql
var schema string

//go:embed 0034_security_rule_outbox.sql
var securityRuleOutboxSchema string

//go:embed 0047_image_deletion_outbox.sql
var imageDeletionOutboxSchema string

//go:embed evaluate.sql
var evaluate string

func Migrate(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0028_project_alerts", schema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0034_security_rule_outbox", securityRuleOutboxSchema); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, "0047_image_deletion_outbox", imageDeletionOutboxSchema)
}

func (s Store) EvaluateNext(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var worked bool
	err := s.db.QueryRowContext(ctx, evaluate).Scan(&worked)
	return worked, err
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
