package nativeconsole

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

type Session struct {
	Grant
	Binding identity.CloudAccountBinding `json:"-"`
}

type sessionAccess struct {
	token, provider, status string
	lock                    bool
}

func sessionRecord(ctx context.Context, tx *sql.Tx, scope identity.Scope, id string, access sessionAccess) (record, error) {
	if access.provider == "openstack" {
		if _, ok := identity.BrowserSessionFromContext(ctx); !ok {
			return record{}, identity.ErrForbidden
		}
	}
	actor, err := authorize(ctx, tx, access.token, scope, identity.Execute)
	if err != nil {
		return record{}, err
	}
	rec, err := getRecord(ctx, tx, actor, id, access.lock)
	if err != nil {
		return record{}, err
	}
	if rec.Requester != actor.Principal {
		return record{}, identity.ErrForbidden
	}
	if rec.Provider != access.provider || rec.Status != access.status {
		return record{}, ErrStale
	}
	if _, err := validateTarget(ctx, tx, actor, rec, access.lock); err != nil {
		return record{}, err
	}
	reviewer := actor
	reviewer.Principal = rec.Reviewer
	member, err := identity.NewStore(tx).AuthorizeProject(ctx, reviewer, identity.Approve)
	if err != nil {
		return record{}, err
	}
	if member.Revision != rec.reviewerRevision {
		return record{}, ErrStale
	}
	return rec, nil
}

func (s Store) Claim(ctx context.Context, scope identity.Scope, id string) (Session, error) {
	return s.claim(ctx, scope, id, sessionAccess{provider: "openstack", status: "approved", lock: true}, 0, nil)
}

func (s Store) ClaimSerial(ctx context.Context, token string, scope identity.Scope, id string, revision int64, hash [32]byte) (Session, error) {
	if revision < 1 {
		return Session{}, ErrInvalid
	}
	return s.claim(ctx, scope, id, sessionAccess{token: token, provider: "aws-ec2", status: "approved", lock: true}, revision, hash[:])
}

func (s Store) claim(ctx context.Context, scope identity.Scope, id string, access sessionAccess, revision int64, keyHash []byte) (Session, error) {
	if !instanceID.MatchString(id) {
		return Session{}, ErrInvalid
	}
	var session Session
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		rec, err := sessionRecord(ctx, tx, scope, id, access)
		if err != nil {
			return err
		}
		if revision != 0 && revision != rec.Revision {
			return ErrStale
		}
		if subtle.ConstantTimeCompare(keyHash, rec.keyHash) != 1 {
			return identity.ErrForbidden
		}
		session.Binding, err = identity.NewStore(tx).CloudAccountBinding(ctx, scope.Tenant, rec.Account)
		if err != nil {
			return err
		}
		if session.Binding.Revision != rec.credentialRevision || session.Binding.Mode != "write_enabled" {
			return ErrStale
		}
		err = scanGrant(tx.QueryRowContext(ctx, `UPDATE cmp_instance_console_grants
			SET status='in_use',revision=revision+1,actor_id=requester_id,expires_at=statement_timestamp()+interval '5 minutes'
			WHERE grant_id=$1::uuid AND expires_at>statement_timestamp() RETURNING `+grantFields, id), &session.Grant)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		return err
	})
	if err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s Store) Check(ctx context.Context, scope identity.Scope, id string) error {
	return s.check(ctx, scope, id, sessionAccess{provider: "openstack", status: "in_use"})
}

func (s Store) CheckSerial(ctx context.Context, token string, scope identity.Scope, id string) error {
	return s.check(ctx, scope, id, sessionAccess{token: token, provider: "aws-ec2", status: "in_use"})
}

func (s Store) check(ctx context.Context, scope identity.Scope, id string, access sessionAccess) error {
	if !instanceID.MatchString(id) {
		return ErrInvalid
	}
	return dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		_, err := sessionRecord(ctx, tx, scope, id, access)
		return err
	})
}

func (s Store) Finish(ctx context.Context, scope identity.Scope, id string, connected bool) error {
	status := "failed"
	if connected {
		status = "closed"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE cmp_instance_console_grants SET status=$4,revision=revision+1,actor_id=NULL
		WHERE (tenant_id,project_id,grant_id)=($1,$2,$3::uuid) AND provider='openstack' AND status='in_use'`, scope.Tenant, scope.Project, id, status)
	return err
}

func (s Store) FinishSerial(ctx context.Context, scope identity.Scope, id string, receipt provider.SerialReceipt, uncertain bool) (Grant, error) {
	status := "failed"
	// 只有厂商明确接收公钥的收据才能进入 key_sent。
	if receipt.RequestID != "" {
		status = "key_sent"
	} else if uncertain {
		status = "dispatch_unknown"
	}
	var item Grant
	err := scanGrant(s.db.QueryRowContext(ctx, `UPDATE cmp_instance_console_grants SET
		status=$4,revision=revision+1,actor_id=requester_id,provider_request_id=NULLIF($5,''),
		expires_at=CASE WHEN $4='key_sent' THEN $6 ELSE expires_at END
		WHERE (tenant_id,project_id,grant_id)=($1,$2,$3::uuid) AND provider='aws-ec2' AND status='in_use'
		RETURNING `+grantFields, scope.Tenant, scope.Project, id, status, receipt.RequestID, receipt.ValidUntil), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, ErrStale
	}
	return item, err
}
