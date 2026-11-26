package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
)

//go:embed 0025_project_audit.sql
var schema string

//go:embed 0027_catalog_audit.sql
var catalogSchema string

const DefaultLimit = 50
const ExportLimit = 2000

var ErrInvalidQuery = errors.New("invalid project audit query")
var ErrExportTooLarge = errors.New("project audit export exceeds the row limit")
var auditID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type Query struct {
	Category, Account, Subject string
	From, Until                *time.Time
	Cursor                     string
	Limit                      int
}

type Event struct {
	ID             string    `json:"id"`
	Category       string    `json:"category"`
	Action         string    `json:"action"`
	SubjectID      string    `json:"subject_id"`
	ResourceKind   string    `json:"resource_kind"`
	Account        string    `json:"account"`
	Region         string    `json:"region,omitempty"`
	Actor          string    `json:"actor,omitempty"`
	RelatedProject string    `json:"related_project,omitempty"`
	Outcome        string    `json:"outcome,omitempty"`
	Revision       int64     `json:"revision,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type Page struct {
	Items []Event `json:"items"`
	Next  string  `json:"next,omitempty"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) Store { return Store{db: db} }

func Migrate(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0025_project_audit", schema); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, "0027_catalog_audit", catalogSchema)
}

type position struct {
	Filter string    `json:"filter"`
	Time   time.Time `json:"time"`
	ID     string    `json:"id"`
}

func auditPosition(scope identity.Scope, q Query) (position, error) {
	if q.Limit < 1 || q.Limit > 200 || len(q.Cursor) > 1024 ||
		!validFilter(q.Account, 128) || !validFilter(q.Subject, 1024) ||
		(q.From != nil && q.From.Year() < 1) || (q.Until != nil && q.Until.Year() < 1) ||
		(q.From != nil && q.Until != nil && !q.From.Before(*q.Until)) {
		return position{}, ErrInvalidQuery
	}
	switch q.Category {
	case "", "account", "ownership", "operation", "approval", "resolution", "observation", "catalog", "alert", "console_access", "billing_budget", "tag_policy", "change_ticket":
	default:
		return position{}, ErrInvalidQuery
	}
	keys, err := json.Marshal(struct {
		Fields      [5]string
		From, Until *time.Time
	}{[5]string{scope.Tenant, scope.Project, q.Category, q.Account, q.Subject}, auditUTC(q.From), auditUTC(q.Until)})
	if err != nil {
		return position{}, ErrInvalidQuery
	}
	hash := sha256.Sum256(keys)
	filter := base64.RawURLEncoding.EncodeToString(hash[:])
	if q.Cursor == "" {
		return position{Filter: filter}, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(q.Cursor)
	var p position
	if err != nil || json.Unmarshal(raw, &p) != nil || p.Filter != filter || p.Time.IsZero() || p.Time.Year() < 1 || !auditID.MatchString(p.ID) {
		return position{}, ErrInvalidQuery
	}
	return p, nil
}

func auditUTC(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func validFilter(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value) && strings.IndexByte(value, 0) < 0
}

func (s Store) List(ctx context.Context, bearer string, scope identity.Scope, q Query) (Page, error) {
	p, err := auditPosition(scope, q)
	if err != nil {
		return Page{}, err
	}
	return s.read(ctx, bearer, scope, q, p)
}

func (s Store) Export(ctx context.Context, bearer string, scope identity.Scope, q Query) ([]Event, error) {
	if q.Cursor != "" {
		return nil, ErrInvalidQuery
	}
	p, err := auditPosition(scope, q)
	if err != nil {
		return nil, err
	}
	q.Limit = ExportLimit
	page, err := s.read(ctx, bearer, scope, q, p)
	if err != nil {
		return nil, err
	}
	if page.Next != "" {
		return nil, ErrExportTooLarge
	}
	return page.Items, nil
}

func (s Store) read(ctx context.Context, bearer string, scope identity.Scope, q Query, p position) (Page, error) {
	page := Page{Items: make([]Event, 0, q.Limit)}
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		auth := identity.NewStore(tx)
		actor, err := auth.AuthenticateRequest(ctx, bearer)
		if err != nil {
			return err
		}
		if actor.Tenant != scope.Tenant {
			return identity.ErrForbidden
		}
		actor.Project = scope.Project
		if _, err := auth.AuthorizeProject(ctx, actor, identity.Manage); err != nil {
			return err
		}
		return listEvents(ctx, tx, actor, q, p, &page)
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

func listEvents(ctx context.Context, tx *sql.Tx, scope identity.Scope, q Query, p position, page *Page) (retErr error) {
	var statement strings.Builder
	statement.WriteString(`SELECT audit_id::text,category,action,subject_id,resource_kind,account_id,region,
		COALESCE(actor_id,''),COALESCE(related_project,''),outcome,revision,occurred_at
		FROM cmp_project_audit WHERE (tenant_id,project_id)=($1,$2)`)
	args := []any{scope.Tenant, scope.Project}
	param := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	for _, entry := range []struct{ column, value string }{
		{"category", q.Category}, {"account_id", q.Account}, {"subject_id", q.Subject},
	} {
		if entry.value != "" {
			statement.WriteString(" AND " + entry.column + "=" + param(entry.value))
		}
	}
	if q.From != nil {
		statement.WriteString(" AND occurred_at >= " + param(*q.From))
	}
	if q.Until != nil {
		statement.WriteString(" AND occurred_at < " + param(*q.Until))
	}
	if p.ID != "" {
		statement.WriteString(" AND (occurred_at,audit_id)<(" + param(p.Time) + "," + param(p.ID) + "::text::uuid)")
	}
	statement.WriteString(" ORDER BY occurred_at DESC,audit_id DESC LIMIT " + param(q.Limit+1))
	rows, err := tx.QueryContext(ctx, statement.String(), args...)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	for rows.Next() {
		if len(page.Items) == q.Limit {
			last := page.Items[q.Limit-1]
			data, err := json.Marshal(position{Filter: p.Filter, Time: last.OccurredAt.UTC(), ID: last.ID})
			if err != nil {
				return err
			}
			page.Next = base64.RawURLEncoding.EncodeToString(data)
			break
		}
		var e Event
		if err := rows.Scan(&e.ID, &e.Category, &e.Action, &e.SubjectID, &e.ResourceKind, &e.Account, &e.Region,
			&e.Actor, &e.RelatedProject, &e.Outcome, &e.Revision, &e.OccurredAt); err != nil {
			return err
		}
		page.Items = append(page.Items, e)
	}
	return rows.Err()
}
