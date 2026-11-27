package delivery

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxAttempts = 10

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var publicID = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

var ErrBinding = errors.New("event destination binding is unavailable or does not match")
var ErrNotFailed = errors.New("failed event is unavailable")

type Config struct {
	ID                   string `json:"id"`
	Tenant               string `json:"tenant"`
	Project              string `json:"project"`
	Endpoint             string `json:"endpoint"`
	Secret               string `json:"secret"`
	IncludeAlerts        bool   `json:"include_alerts,omitempty"`
	IncludeChangeTickets bool   `json:"include_change_tickets,omitempty"`
}

type Destination struct {
	id, tenant, project  string
	endpoint, secret     string
	endpointHash         [32]byte
	includeAlerts        bool
	includeChangeTickets bool
}

func NewDestination(cfg Config) (Destination, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Fragment != "" ||
		u.RawQuery != "" || u.ForceQuery || strings.Contains(cfg.Endpoint, "#") || len(cfg.Endpoint) > 2048 ||
		!identifier.MatchString(cfg.ID) || !identifier.MatchString(cfg.Tenant) || !identifier.MatchString(cfg.Project) ||
		len(cfg.Secret) < 32 || len(cfg.Secret) > 512 || strings.TrimSpace(cfg.Secret) != cfg.Secret {
		return Destination{}, ErrBinding
	}
	d := Destination{id: cfg.ID, tenant: cfg.Tenant, project: cfg.Project, endpoint: u.String(), secret: cfg.Secret,
		includeAlerts: cfg.IncludeAlerts, includeChangeTickets: cfg.IncludeChangeTickets}
	d.endpointHash = sha256.Sum256([]byte(d.endpoint))
	return d, nil
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) Store { return Store{db: db} }

func (s Store) Register(ctx context.Context, dest Destination) error {
	var id string
	err := s.db.QueryRowContext(ctx, `INSERT INTO cmp_operation_event_sinks AS existing
		(sink_id,tenant_id,project_id,endpoint_hash,include_alerts,include_change_tickets)
		SELECT $1,tenant_id,project_id,$4,$5,$6 FROM cmp_projects WHERE (tenant_id,project_id)=($2,$3)
		ON CONFLICT (sink_id) DO UPDATE SET enabled=true,include_alerts=EXCLUDED.include_alerts,include_change_tickets=EXCLUDED.include_change_tickets
		WHERE (existing.tenant_id,existing.project_id,existing.endpoint_hash)=(EXCLUDED.tenant_id,EXCLUDED.project_id,EXCLUDED.endpoint_hash)
		RETURNING sink_id`, dest.id, dest.tenant, dest.project, dest.endpointHash[:], dest.includeAlerts, dest.includeChangeTickets).Scan(&id)
	return bindingError(err)
}

func (s Store) checkBinding(ctx context.Context, dest Destination) error {
	return checkDestinationBinding(ctx, s.db, dest)
}

type deliveryBindingQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func checkDestinationBinding(ctx context.Context, db deliveryBindingQuery, dest Destination) error {
	var id string
	err := db.QueryRowContext(ctx, `SELECT sink_id FROM cmp_operation_event_sinks
		WHERE (sink_id,tenant_id,project_id,endpoint_hash)=($1,$2,$3,$4)`, dest.id, dest.tenant, dest.project, dest.endpointHash[:]).Scan(&id)
	return bindingError(err)
}

func (s Store) Disable(ctx context.Context, dest Destination) error {
	var id string
	err := s.db.QueryRowContext(ctx, `UPDATE cmp_operation_event_sinks SET enabled=false
		WHERE (sink_id,tenant_id,project_id,endpoint_hash)=($1,$2,$3,$4) RETURNING sink_id`, dest.id, dest.tenant, dest.project, dest.endpointHash[:]).Scan(&id)
	return bindingError(err)
}

func bindingError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBinding
	}
	return err
}

type claim struct {
	id, sink, token, tenant, project string
	eventType                        string
	attempts                         int
	body, endpointHash               []byte
	exhausted                        bool
}

func (c claim) matches(d Destination) bool {
	return c.sink == d.id && c.tenant == d.tenant && c.project == d.project && bytes.Equal(c.endpointHash, d.endpointHash[:])
}

func (s Store) claim(ctx context.Context, sinks []string) (claim, error) {
	c := claim{token: rand.Text()}
	err := s.db.QueryRowContext(ctx, `WITH due AS (
		SELECT o.sink_id,o.public_id,s.tenant_id,s.project_id,s.endpoint_hash FROM cmp_operation_event_outbox AS o
		JOIN cmp_operation_event_sinks AS s USING (sink_id)
		WHERE s.enabled AND s.sink_id=ANY($1::text[]) AND o.delivered_at IS NULL AND o.failed_at IS NULL
		AND o.available_at<=statement_timestamp() AND (o.lease_until IS NULL OR o.lease_until<=statement_timestamp())
		ORDER BY o.available_at,o.sequence NULLS LAST,o.public_id,o.sink_id LIMIT 1 FOR UPDATE OF o SKIP LOCKED FOR SHARE OF s SKIP LOCKED
	) UPDATE cmp_operation_event_outbox AS o SET
		attempts=LEAST(o.attempts+1,$2),
		lease_token=CASE WHEN o.attempts<$2 THEN $3::text END,
		lease_until=CASE WHEN o.attempts<$2 THEN clock_timestamp()+interval '30 seconds' END,
		failed_at=CASE WHEN o.attempts>=$2 THEN clock_timestamp() END,
		failure=CASE WHEN o.attempts>=$2 THEN 'lease_exhausted' ELSE o.failure END
	FROM due WHERE (o.sink_id,o.public_id)=(due.sink_id,due.public_id)
	RETURNING o.public_id::text,o.sink_id,o.body,o.event_type,o.attempts,due.tenant_id,due.project_id,due.endpoint_hash,o.failed_at IS NOT NULL`,
		sinks, maxAttempts, c.token).Scan(&c.id, &c.sink, &c.body, &c.eventType, &c.attempts, &c.tenant, &c.project, &c.endpointHash, &c.exhausted)
	return c, err
}

type settlement struct {
	state, reason string
	retryDelay    time.Duration
	httpStatus    int
}

func (s Store) settle(ctx context.Context, c claim, result settlement) (bool, error) {
	var deliveredID string
	err := s.db.QueryRowContext(ctx, `UPDATE cmp_operation_event_outbox SET
		delivered_at=CASE WHEN $4='delivered' THEN clock_timestamp() END,
		failed_at=CASE WHEN $4='failed' THEN clock_timestamp() END,
		available_at=CASE WHEN $4='retry' THEN clock_timestamp()+$5*interval '1 millisecond' ELSE available_at END,
		failure=NULLIF($6,''),http_status=NULLIF($7,0),lease_token=NULL,lease_until=NULL
		WHERE (sink_id,public_id,lease_token)=($1,$2::uuid,$3) AND lease_until>clock_timestamp()
		AND delivered_at IS NULL AND failed_at IS NULL RETURNING public_id::text`,
		c.sink, c.id, c.token, result.state, result.retryDelay.Milliseconds(), result.reason, result.httpStatus).Scan(&deliveredID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

type FailedEvent struct {
	ID         string    `json:"id"`
	Attempts   int       `json:"attempts"`
	FailedAt   time.Time `json:"failed_at"`
	Reason     string    `json:"reason"`
	HTTPStatus *int      `json:"http_status,omitempty"`
}

func (s Store) Failures(ctx context.Context, dest Destination) (result []FailedEvent, retErr error) {
	if err := s.checkBinding(ctx, dest); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.public_id::text,o.attempts,o.failed_at,COALESCE(o.failure,'unknown'),o.http_status
		FROM cmp_operation_event_outbox AS o JOIN cmp_operation_event_sinks AS s USING (sink_id)
		WHERE (s.sink_id,s.tenant_id,s.project_id,s.endpoint_hash)=($1,$2,$3,$4) AND o.failed_at IS NOT NULL
		ORDER BY o.failed_at DESC,o.sequence DESC NULLS LAST,o.public_id DESC LIMIT 100`, dest.id, dest.tenant, dest.project, dest.endpointHash[:])
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	result = []FailedEvent{}
	for rows.Next() {
		var e FailedEvent
		if err := rows.Scan(&e.ID, &e.Attempts, &e.FailedAt, &e.Reason, &e.HTTPStatus); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

func (s Store) Retry(ctx context.Context, dest Destination, eventID string) error {
	if !publicID.MatchString(eventID) {
		return ErrNotFailed
	}
	var retriedID string
	err := s.db.QueryRowContext(ctx, `UPDATE cmp_operation_event_outbox AS o SET attempts=0,available_at=clock_timestamp(),failed_at=NULL,failure=NULL,http_status=NULL
		FROM cmp_operation_event_sinks AS s WHERE o.sink_id=s.sink_id AND s.enabled
		AND (s.sink_id,s.tenant_id,s.project_id,s.endpoint_hash)=($1,$2,$3,$4) AND o.public_id=$5::uuid
		AND o.failed_at IS NOT NULL RETURNING o.public_id::text`, dest.id, dest.tenant, dest.project, dest.endpointHash[:], eventID).Scan(&retriedID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFailed
	}
	return err
}
