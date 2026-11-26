package operations

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

var opaqueTicketID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type TicketEvidenceInput struct {
	TicketID       string     `json:"ticket_id"`
	SourceRevision int64      `json:"source_revision,string"`
	Decision       string     `json:"decision"`
	ValidUntil     *time.Time `json:"valid_until,omitempty"`
}

type TicketEvidence struct {
	TicketEvidenceInput
	RecordedAt time.Time `json:"recorded_at"`
}

type ChangeTicket struct {
	PolicyRevision int64           `json:"policy_revision,string"`
	Ready          bool            `json:"ready"`
	Evidence       *TicketEvidence `json:"evidence,omitempty"`
}

func (s Store) GetChangeTicket(ctx context.Context, token string, scope identity.Scope, id string) (ticket ChangeTicket, err error) {
	if !operationKey.MatchString(id) {
		return ticket, ErrInvalid
	}
	err = dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		if _, err := operationActor(ctx, tx, token, scope, identity.View); err != nil {
			return err
		}
		ticket, err = readChangeTicket(ctx, tx, scope, id)
		return err
	})
	return ticket, err
}

func (s Store) RecordChangeTicket(ctx context.Context, token string, scope identity.Scope, id string, input TicketEvidenceInput) (ticket ChangeTicket, err error) {
	if !operationKey.MatchString(id) || !opaqueTicketID.MatchString(input.TicketID) || input.SourceRevision < 1 ||
		!((input.Decision == "approved" && input.ValidUntil != nil) || (input.Decision == "revoked" && input.ValidUntil == nil)) {
		return ticket, ErrInvalid
	}
	if input.ValidUntil != nil {
		expiration := input.ValidUntil.UTC().Truncate(time.Microsecond)
		input.ValidUntil = &expiration
	}
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		if err := recordTicketEvidence(ctx, tx, token, scope, id, input); err != nil {
			return err
		}
		ticket, err = readChangeTicket(ctx, tx, scope, id)
		return err
	})
	return ticket, err
}

func recordTicketEvidence(ctx context.Context, tx *sql.Tx, token string, scope identity.Scope, id string, input TicketEvidenceInput) error {
	verifier, err := identity.NewStore(tx).AuthenticateService(ctx, token)
	if err != nil {
		return err
	}
	if verifier.Tenant != scope.Tenant {
		return identity.ErrForbidden
	}
	var designated, requester, status string
	err = tx.QueryRowContext(ctx, `SELECT policy.verifier_id,op.principal_id,op.status
		FROM cmp_instance_operations AS op JOIN cmp_change_ticket_policies AS policy
		ON (policy.tenant_id,policy.project_id,policy.revision)=(op.tenant_id,op.project_id,op.ticket_policy_revision)
		WHERE (op.tenant_id,op.project_id,op.operation_id)=($1,$2,$3) FOR UPDATE OF op`,
		scope.Tenant, scope.Project, id).Scan(&designated, &requester, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrForbidden
	}
	if err != nil {
		return err
	}
	if verifier.Principal != designated || verifier.Principal == requester {
		return identity.ErrForbidden
	}
	membership, principal, err := lockTicketVerifier(ctx, tx, scope, designated)
	if err != nil {
		return err
	}
	current, err := readTicketEvidence(ctx, tx, id)
	if err != nil {
		return err
	}
	if current != nil {
		if input.TicketID != current.TicketID || input.SourceRevision < current.SourceRevision {
			return ErrConflict
		}
		if input.SourceRevision == current.SourceRevision {
			if input.Decision == current.Decision && sameTicketExpiration(input.ValidUntil, current.ValidUntil) {
				return nil
			}
			return ErrConflict
		}
	}
	if status == "succeeded" || status == "failed" {
		return ErrConflict
	}
	if current == nil {
		result, err := tx.ExecContext(ctx, `INSERT INTO cmp_change_ticket_links (operation_id,tenant_id,project_id,ticket_id)
			VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, id, scope.Tenant, scope.Project, input.TicketID)
		if err := ticketWriteResult(result, err, ErrConflict); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO cmp_change_ticket_receipts
		(operation_id,source_revision,decision,valid_until,verifier_revision,principal_revision)
		SELECT $1,$2,$3,$4,$5,$6 WHERE $4::timestamptz IS NULL OR
			$4::timestamptz>statement_timestamp() AND $4::timestamptz<=statement_timestamp()+interval '24 hours'`,
		id, input.SourceRevision, input.Decision, input.ValidUntil, membership, principal)
	return ticketWriteResult(result, err, ErrInvalid)
}

func isTicketGuardError(err error) bool {
	var state interface{ SQLState() string }
	return errors.As(err, &state) && state.SQLState() == "PT409"
}

func lockTicketVerifier(ctx context.Context, tx *sql.Tx, scope identity.Scope, id string) (membership, principal int64, err error) {
	err = tx.QueryRowContext(ctx, `SELECT m.revision,p.ticket_auth_revision FROM cmp_memberships AS m
		JOIN cmp_principals AS p USING (tenant_id,principal_id)
		WHERE (m.tenant_id,m.project_id,m.principal_id)=($1,$2,$3)
			AND m.active AND m.role='viewer' AND p.active AND p.kind='service' FOR SHARE OF m,p`,
		scope.Tenant, scope.Project, id).Scan(&membership, &principal)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrForbidden
	}
	return membership, principal, err
}

func readChangeTicket(ctx context.Context, tx *sql.Tx, scope identity.Scope, id string) (ChangeTicket, error) {
	var ticket ChangeTicket
	var revision sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT op.ticket_policy_revision,cmp_change_ticket_valid(op) FROM cmp_instance_operations AS op
		WHERE (op.tenant_id,op.project_id,op.operation_id)=($1,$2,$3)`, scope.Tenant, scope.Project, id).Scan(&revision, &ticket.Ready)
	if errors.Is(err, sql.ErrNoRows) {
		return ticket, identity.ErrForbidden
	}
	if err != nil {
		return ticket, err
	}
	if !revision.Valid {
		return ticket, ErrTicketNotFound
	}
	ticket.PolicyRevision = revision.Int64
	ticket.Evidence, err = readTicketEvidence(ctx, tx, id)
	return ticket, err
}

func readTicketEvidence(ctx context.Context, tx *sql.Tx, id string) (*TicketEvidence, error) {
	var evidence TicketEvidence
	err := tx.QueryRowContext(ctx, `SELECT link.ticket_id,receipt.source_revision,receipt.decision,receipt.valid_until,receipt.recorded_at
		FROM cmp_change_ticket_links AS link JOIN cmp_change_ticket_receipts AS receipt USING (operation_id)
		WHERE link.operation_id=$1 ORDER BY receipt.source_revision DESC LIMIT 1`, id).Scan(
		&evidence.TicketID, &evidence.SourceRevision, &evidence.Decision, &evidence.ValidUntil, &evidence.RecordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &evidence, err
}

func ticketWriteResult(result sql.Result, err, empty error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if count == 0 && err == nil {
		return empty
	}
	return err
}

func sameTicketExpiration(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func requireChangeTicket(ctx context.Context, tx *sql.Tx, scope identity.Scope, op Operation) error {
	if op.TicketPolicyRevision == 0 {
		return nil
	}
	var verifier string
	if err := tx.QueryRowContext(ctx, `SELECT verifier_id FROM cmp_change_ticket_policies
		WHERE (tenant_id,project_id,revision)=($1,$2,$3)`, scope.Tenant, scope.Project, op.TicketPolicyRevision).Scan(&verifier); err != nil {
		return err
	}
	if _, _, err := lockTicketVerifier(ctx, tx, scope, verifier); err != nil {
		if errors.Is(err, identity.ErrForbidden) {
			return ErrTicketRequired
		}
		return err
	}
	var ready bool
	err := tx.QueryRowContext(ctx, `SELECT cmp_change_ticket_valid(op) FROM cmp_instance_operations AS op
		WHERE operation_id=$1`, op.ID).Scan(&ready)
	if err == nil && !ready {
		return ErrTicketRequired
	}
	return err
}
