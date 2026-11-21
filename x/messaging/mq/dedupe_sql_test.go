package mq

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/data/idempotency"
)

type dedupeTestConn struct {
	driver.Conn
	row                []driver.Value
	queryErr, writeErr error
	key                string
	writes             int
	insertArgs         []driver.NamedValue
}

func (*dedupeTestConn) Close() error { return nil }

func (c *dedupeTestConn) QueryContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
	c.key = args[0].Value.(string)
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	return &dedupeTestRow{values: c.row}, nil
}

func (c *dedupeTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.writes++
	if c.writeErr != nil {
		return nil, c.writeErr
	}
	if strings.HasPrefix(query, "INSERT") {
		c.insertArgs = slices.Clone(args)
	}
	return driver.RowsAffected(1), nil
}

type dedupeTestConnector struct {
	driver.Connector
	conn *dedupeTestConn
}

func (c dedupeTestConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }

type dedupeTestRow struct {
	values []driver.Value
}

func (*dedupeTestRow) Columns() []string {
	return []string{"key", "request_hash", "status", "response", "created_at", "updated_at", "expires_at"}
}
func (*dedupeTestRow) Close() error { return nil }
func (r *dedupeTestRow) Next(values []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(values, r.values)
	r.values = nil
	return nil
}

func TestSQLDeduperCompletionContract(t *testing.T) {
	now := time.Unix(123, 0).UTC()
	conn := &dedupeTestConn{}
	db := sql.OpenDB(dedupeTestConnector{conn: conn})
	defer db.Close()
	d := NewSQLDeduper(db, SQLDeduperConfig{Prefix: " fixture ", Now: func() time.Time { return now }})
	ctx := t.Context()
	sentinel := errors.New("dedupe unavailable")
	for _, tc := range []struct {
		name, status string
		expires      time.Time
		queryErr     error
		want         bool
	}{
		{name: "missing"},
		{name: "unfinished", status: string(idempotency.StatusInProgress), expires: now.Add(time.Minute)},
		{name: "completed", status: string(idempotency.StatusCompleted), expires: now.Add(time.Minute), want: true},
		{name: "expired", status: string(idempotency.StatusCompleted), expires: now},
		{name: "driver error", queryErr: sentinel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn.row = nil
			if tc.status != "" {
				conn.row = []driver.Value{"fixture:one", "hash", tc.status, nil, now, now, tc.expires}
			}
			conn.queryErr = tc.queryErr
			got, err := d.IsCompleted(ctx, " one ")
			if got != tc.want || !errors.Is(err, tc.queryErr) || conn.key != "fixture:one" {
				t.Errorf("completed=%t, err=%v, key=%q", got, err, conn.key)
			}
		})
	}
	for _, tc := range []struct{ ttl, want time.Duration }{{0, 24 * time.Hour}, {time.Minute, time.Minute}} {
		if err := d.MarkCompleted(ctx, "one", tc.ttl); err != nil {
			t.Fatal(err)
		}
		args := conn.insertArgs
		if len(args) != 7 || args[0].Value != "fixture:one" || args[1].Value != "mq_task_dedupe" ||
			args[2].Value != string(idempotency.StatusCompleted) || args[6].Value != now.Add(tc.want) {
			t.Errorf("completion args=%v for ttl %v", args, tc.ttl)
		}
	}
	writes := conn.writes
	if err := d.MarkCompleted(ctx, " ", 0); !errors.Is(err, ErrInvalidConfig) || conn.writes != writes {
		t.Errorf("invalid key: %v, writes=%d", err, conn.writes)
	}
	if _, err := d.IsCompleted(ctx, " "); !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("invalid lookup key: %v", err)
	}
	conn.writeErr = sentinel
	if err := d.MarkCompleted(ctx, "one", 0); !errors.Is(err, sentinel) {
		t.Errorf("write error=%v", err)
	}
}
