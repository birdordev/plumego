package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/dbschema"
)

var ErrUnauthenticated = errors.New("authentication required")

func MigrateServiceTokens(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0003_service_tokens")
}

func (s Store) AuthenticateService(ctx context.Context, token string) (Scope, error) {
	if len(token) < 32 || len(token) > 512 {
		return Scope{}, ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(token))
	var scope Scope
	err := s.db.QueryRowContext(ctx, `SELECT t.tenant_id,t.principal_id
		FROM cmp_service_tokens AS t JOIN cmp_principals AS p USING (tenant_id,principal_id)
		WHERE t.token_hash=$1 AND t.expires_at>statement_timestamp()
		  AND t.revoked_at IS NULL AND p.active AND p.kind='service'`, digest[:]).
		Scan(&scope.Tenant, &scope.Principal)
	if errors.Is(err, sql.ErrNoRows) {
		return Scope{}, ErrUnauthenticated
	}
	return scope, err
}
