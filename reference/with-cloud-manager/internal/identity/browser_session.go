package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
)

//go:embed 0024_browser_sessions.sql
var browserSessionSchema string

const BrowserSessionTTL = time.Hour

type browserSessionKey struct{}

func WithBrowserSession(ctx context.Context, credential string) context.Context {
	return context.WithValue(ctx, browserSessionKey{}, credential)
}

func BrowserSessionFromContext(ctx context.Context) (string, bool) {
	credential, ok := ctx.Value(browserSessionKey{}).(string)
	return credential, ok && ValidBrowserCredential(credential)
}

func ValidBrowserCredential(credential string) bool {
	const size = 32
	if len(credential) != base64.RawURLEncoding.EncodedLen(size) {
		return false
	}
	var raw [size]byte
	sizeRead, err := base64.RawURLEncoding.Strict().Decode(raw[:], []byte(credential))
	return err == nil && sizeRead == size
}

func (s Store) AuthenticateRequest(ctx context.Context, bearer string) (Scope, error) {
	if credential, ok := BrowserSessionFromContext(ctx); ok {
		if bearer != "" {
			return Scope{}, ErrUnauthenticated
		}
		return s.authenticateBrowser(ctx, credential)
	}
	return s.AuthenticateService(ctx, bearer)
}

func (s Store) authenticateBrowser(ctx context.Context, credential string) (Scope, error) {
	if !ValidBrowserCredential(credential) {
		return Scope{}, ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(credential))
	var scope Scope
	err := s.db.QueryRowContext(ctx, `SELECT session.tenant_id,session.principal_id
		FROM cmp_browser_sessions AS session JOIN cmp_principals AS p USING (tenant_id,principal_id)
		WHERE session.session_hash=$1 AND session.expires_at>statement_timestamp() AND p.active AND p.kind='human'`,
		digest[:]).Scan(&scope.Tenant, &scope.Principal)
	if errors.Is(err, sql.ErrNoRows) {
		return Scope{}, ErrUnauthenticated
	}
	return scope, err
}

type BrowserSessions struct{ db *sql.DB }

func NewBrowserSessions(db *sql.DB) BrowserSessions { return BrowserSessions{db: db} }

func MigrateBrowserSessions(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0024_browser_sessions", browserSessionSchema)
}

func (s BrowserSessions) Login(ctx context.Context, tenant, issuer, subject, previous string) (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	credential := base64.RawURLEncoding.EncodeToString(raw[:])
	digest := sha256.Sum256([]byte(credential))
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		var principal string
		err := tx.QueryRowContext(ctx, `SELECT principal_id FROM cmp_principals AS p
			WHERE (p.tenant_id,p.issuer,p.subject)=($1,$2,$3) AND p.kind='human' AND p.active
			AND EXISTS (SELECT 1 FROM cmp_memberships AS m WHERE (m.tenant_id,m.principal_id)=(p.tenant_id,p.principal_id) AND m.active)
			FOR SHARE OF p`, tenant, issuer, subject).Scan(&principal)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if ValidBrowserCredential(previous) {
			old := sha256.Sum256([]byte(previous))
			if _, err := tx.ExecContext(ctx, "DELETE FROM cmp_browser_sessions WHERE session_hash=$1", old[:]); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cmp_browser_sessions (session_hash,tenant_id,principal_id,expires_at)
			VALUES ($1,$2,$3,statement_timestamp()+($4::bigint*interval '1 second'))`,
			digest[:], tenant, principal, int64(BrowserSessionTTL/time.Second))
		return err
	})
	if err != nil {
		return "", err
	}
	return credential, nil
}

func (s BrowserSessions) Logout(ctx context.Context, credential string) error {
	if !ValidBrowserCredential(credential) {
		return ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(credential))
	_, err := s.db.ExecContext(ctx, "DELETE FROM cmp_browser_sessions WHERE session_hash=$1", digest[:])
	return err
}

type BrowserProject struct {
	ID   string `json:"id"`
	Role Role   `json:"role"`
}

type BrowserProfile struct {
	Tenant    string           `json:"tenant"`
	Principal string           `json:"principal"`
	Projects  []BrowserProject `json:"projects"`
}

func (s BrowserSessions) Profile(ctx context.Context, credential string) (BrowserProfile, error) {
	var profile BrowserProfile
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) (retErr error) {
		actor, err := NewStore(tx).authenticateBrowser(ctx, credential)
		if err != nil {
			return err
		}
		profile = BrowserProfile{Tenant: actor.Tenant, Principal: actor.Principal, Projects: []BrowserProject{}}
		rows, err := tx.QueryContext(ctx, `SELECT project_id,role FROM cmp_memberships
			WHERE (tenant_id,principal_id)=($1,$2) AND active ORDER BY project_id`, actor.Tenant, actor.Principal)
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, rows.Close()) }()
		for rows.Next() {
			var project BrowserProject
			if err := rows.Scan(&project.ID, &project.Role); err != nil {
				return err
			}
			profile.Projects = append(profile.Projects, project)
		}
		return rows.Err()
	})
	return profile, err
}

func (s BrowserSessions) PurgeExpired(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `WITH due AS (
		SELECT session_hash FROM cmp_browser_sessions WHERE expires_at<=statement_timestamp()
		ORDER BY expires_at LIMIT 1000 FOR UPDATE SKIP LOCKED)
		DELETE FROM cmp_browser_sessions AS session USING due WHERE session.session_hash=due.session_hash`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
