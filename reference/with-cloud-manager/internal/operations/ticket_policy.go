package operations

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

//go:embed 0101_change_tickets.sql
var changeTicketSchema string

//go:embed 0102_change_ticket_events.sql
var changeTicketEventsSchema string

var (
	ErrTicketNotFound = errors.New("change ticket policy not found")
	ErrTicketStale    = errors.New("change ticket policy version is stale")
	ErrTicketRequired = errors.New("current external change approval required")
)

type TicketPolicyInput struct {
	Actions  []string `json:"actions"`
	Verifier string   `json:"verifier"`
}

type TicketPolicy struct {
	TicketPolicyInput
	Revision    int64     `json:"revision,string"`
	PublishedBy string    `json:"published_by"`
	PublishedAt time.Time `json:"published_at"`
}

func migrateChangeTickets(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0101_change_tickets", changeTicketSchema)
}

func MigrateChangeTicketEvents(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0102_change_ticket_events", changeTicketEventsSchema)
}

func (s Store) GetTicketPolicy(ctx context.Context, token string, scope identity.Scope, revision int64) (policy TicketPolicy, err error) {
	if revision < 0 {
		return policy, ErrInvalid
	}
	err = dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		if _, err := operationActor(ctx, tx, token, scope, identity.Manage); err != nil {
			return err
		}
		var actions []byte
		err := tx.QueryRowContext(ctx, `SELECT revision,to_json(actions),verifier_id,published_by,published_at
			FROM cmp_change_ticket_policies WHERE (tenant_id,project_id)=($1,$2) AND ($3::bigint=0 OR revision=$3)
			ORDER BY revision DESC LIMIT 1`, scope.Tenant, scope.Project, revision).Scan(
			&policy.Revision, &actions, &policy.Verifier, &policy.PublishedBy, &policy.PublishedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrTicketNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(actions, &policy.Actions)
	})
	return policy, err
}

func (s Store) PublishTicketPolicy(ctx context.Context, token string, scope identity.Scope, expected int64, input TicketPolicyInput) (policy TicketPolicy, err error) {
	if expected < 0 || !validTicketPolicy(input) {
		return policy, ErrInvalid
	}
	input.Actions = slices.Clone(input.Actions)
	slices.Sort(input.Actions)
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		admin, err := operationActor(ctx, tx, token, scope, identity.Manage)
		if err != nil {
			return err
		}
		var project string
		if err := tx.QueryRowContext(ctx, `SELECT project_id FROM cmp_projects
			WHERE (tenant_id,project_id)=($1,$2) FOR NO KEY UPDATE`, scope.Tenant, scope.Project).Scan(&project); err != nil {
			return err
		}
		var publisher string
		err = tx.QueryRowContext(ctx, `SELECT p.principal_id FROM cmp_memberships AS m
			JOIN cmp_principals AS p USING (tenant_id,principal_id)
			WHERE (m.tenant_id,m.project_id,m.principal_id)=($1,$2,$3) AND m.active AND p.active AND m.role='admin'
			FOR SHARE OF m,p`, scope.Tenant, scope.Project, admin.Principal).Scan(&publisher)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(revision),0) FROM cmp_change_ticket_policies
			WHERE (tenant_id,project_id)=($1,$2)`, scope.Tenant, scope.Project).Scan(&version); err != nil {
			return err
		}
		if expected == 0 && version != 0 {
			return ErrConflict
		}
		if version != expected {
			return ErrTicketStale
		}
		if len(input.Actions) > 0 {
			if _, _, err := lockTicketVerifier(ctx, tx, scope, input.Verifier); err != nil {
				return err
			}
		} else {
			// 停用版本保留上一个核验主体，便于核对历史。
			if err := tx.QueryRowContext(ctx, `SELECT verifier_id FROM cmp_change_ticket_policies
				WHERE (tenant_id,project_id,revision)=($1,$2,$3)`, scope.Tenant, scope.Project, version).Scan(&input.Verifier); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return ErrInvalid
				}
				return err
			}
		}
		policy = TicketPolicy{TicketPolicyInput: input, Revision: version + 1, PublishedBy: publisher}
		return tx.QueryRowContext(ctx, `INSERT INTO cmp_change_ticket_policies
			(tenant_id,project_id,revision,actions,verifier_id,published_by)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING published_at`,
			scope.Tenant, scope.Project, policy.Revision, input.Actions, input.Verifier, publisher).Scan(&policy.PublishedAt)
	})
	return policy, err
}

func validTicketPolicy(input TicketPolicyInput) bool {
	if input.Actions == nil || len(input.Actions) > 64 || (len(input.Actions) > 0 && !operationKey.MatchString(input.Verifier)) {
		return false
	}
	seen := make(map[string]bool, len(input.Actions))
	for _, action := range input.Actions {
		if (action != "*" && !ticketPolicyAction(action)) || (action == "*" && len(input.Actions) != 1) || seen[action] {
			return false
		}
		seen[action] = true
	}
	return true
}

func ticketPolicyAction(action string) bool {
	switch action {
	case provider.StageBareMetalPXE, provider.CancelBareMetalPXE, powerOnBareMetal, shutdownBareMetal, "start", "stop", "reboot", "delete", "create", "attach-volume", "detach-volume", retireInstance,
		"create-network", "delete-network", "create-subnet", "delete-subnet", "create-security-group",
		"delete-security-group", "create-security-group-rule", "revoke-security-group-rule",
		createElasticIP, releaseElasticIP, bindElasticIP, unbindElasticIP,
		createSnapshot, deleteSnapshot, rollbackSnapshot, restoreSnapshot, createImage, copyImage, deleteImage,
		scaleWorkload, createDNSRecord, setDNSRecordStatus, deleteDNSRecord,
		renameLoadBalancer, renameLoadBalancerListener, renameLoadBalancerTargetGroup, renameDatabaseInstance, renameCacheInstance,
		setLoadBalancerBackendWeight, detachLoadBalancerBackend, attachLoadBalancerBackend:
		return true
	default:
		return false
	}
}

func currentTicketActions(ctx context.Context, tx *sql.Tx, scope identity.Scope) ([]string, error) {
	var encoded []byte
	err := tx.QueryRowContext(ctx, `SELECT to_json(actions) FROM cmp_change_ticket_policies
		WHERE (tenant_id,project_id)=($1,$2) ORDER BY revision DESC LIMIT 1`, scope.Tenant, scope.Project).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var actions []string
	err = json.Unmarshal(encoded, &actions)
	return actions, err
}
