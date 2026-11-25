package identity

import (
	"context"
	"database/sql"
	"errors"
)

type CloudAccountBinding struct {
	Provider       string
	RemoteIdentity string
	CredentialRef  string
	Revision       int64
	Mode           string
}

func (s Store) CloudAccountBinding(ctx context.Context, tenant, account string) (CloudAccountBinding, error) {
	var binding CloudAccountBinding
	err := s.db.QueryRowContext(ctx, `SELECT provider,COALESCE(remote_identity,''),credential_ref,credential_revision,mode
		FROM cmp_cloud_accounts WHERE tenant_id=$1 AND account_id=$2 AND mode IN ('pending','read_only','write_enabled')`,
		tenant, account).Scan(&binding.Provider, &binding.RemoteIdentity, &binding.CredentialRef, &binding.Revision, &binding.Mode)
	if errors.Is(err, sql.ErrNoRows) {
		return CloudAccountBinding{}, ErrForbidden
	}
	return binding, err
}

func (s Store) VerifyCloudAccount(ctx context.Context, tenant, account string, binding CloudAccountBinding, remote string) error {
	if remote == "" {
		return ErrForbidden
	}
	var revision int64
	err := s.db.QueryRowContext(ctx, `UPDATE cmp_cloud_accounts SET remote_identity=$6,
		mode=CASE WHEN mode='pending' THEN 'read_only' ELSE mode END
		WHERE tenant_id=$1 AND account_id=$2 AND provider=$3 AND credential_ref=$4 AND credential_revision=$5
		  AND (remote_identity IS NULL OR remote_identity=$6) AND mode IN ('pending','read_only','write_enabled')
		RETURNING credential_revision`, tenant, account, binding.Provider, binding.CredentialRef, binding.Revision, remote).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	return err
}
