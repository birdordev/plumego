package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/stdlib"
)

type lostCommitResponse struct {
	armed   atomic.Bool
	dropped chan error
}

func (f *lostCommitResponse) TraceQueryStart(ctx context.Context, _ *pgx.Conn, query pgx.TraceQueryStartData) context.Context {
	if query.SQL == "commit" {
		f.armed.Store(true)
	}
	return ctx
}

func (*lostCommitResponse) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

type commitResponseReader struct {
	io.Reader
	fault        *lostCommitResponse
	disconnected bool
}

func (r *commitResponseReader) Read(p []byte) (int, error) {
	if r.disconnected {
		return 0, io.EOF
	}
	if !r.fault.armed.Swap(false) {
		return r.Reader.Read(p)
	}
	// Require PostgreSQL's real success response before simulating a lost connection.
	message, err := pgproto3.NewFrontend(r.Reader, io.Discard).Receive()
	if err == nil {
		complete, ok := message.(*pgproto3.CommandComplete)
		if !ok || string(complete.CommandTag) != "COMMIT" {
			err = fmt.Errorf("expected PostgreSQL COMMIT confirmation, received %T", message)
		}
	}
	r.fault.dropped <- err
	r.disconnected = true
	return 0, io.ErrUnexpectedEOF
}

func commitDroppingDB(t *testing.T, db *sql.DB) (*sql.DB, *lostCommitResponse) {
	t.Helper()
	ctx := t.Context()
	var schema string
	if err := db.QueryRowContext(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgx.ParseConfig(os.Getenv("PLUMEGO_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = pgx.Identifier{schema}.Sanitize()
	fault := &lostCommitResponse{dropped: make(chan error, 1)}
	cfg.Tracer = fault
	cfg.BuildFrontend = func(r io.Reader, w io.Writer) *pgproto3.Frontend {
		return pgproto3.NewFrontend(&commitResponseReader{Reader: r, fault: fault}, w)
	}
	disruptedDB := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		if err := disruptedDB.Close(); err != nil {
			t.Error(err)
		}
	})
	return disruptedDB, fault
}

func TestPostgresSubmitRecoversLostCommitResponse(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	disruptedDB, fault := commitDroppingDB(t, db)
	app, err := newApp("127.0.0.1:0", "test-token", "tenant", repository{disruptedDB, mqstore.NewSQL(disruptedDB, mqstore.DefaultSQLConfig())})
	if err != nil {
		t.Fatal(err)
	}
	const key = "lost-commit"
	const body = `{"topic":"demo.record","payload":{"message":"original"}}`
	first := auditHTTPRequest(ctx, app, http.MethodPost, "/runs", key, "", body)
	select {
	case err := <-fault.dropped:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("submission did not reach the PostgreSQL commit fault")
	}
	if first.Code != http.StatusServiceUnavailable || first.Header().Get("Location") != "" ||
		!strings.Contains(first.Body.String(), "retry with the same idempotency key") {
		t.Fatalf("uncertain response = %d %s %s", first.Code, first.Header(), first.Body)
	}

	var id string
	if err := db.QueryRowContext(ctx, `SELECT id FROM scheduled_runs WHERE tenant_id = 'tenant' AND request_key = $1`, key).Scan(&id); err != nil {
		t.Fatal(err)
	}
	recovered, err := newApp("127.0.0.1:0", "test-token", "tenant", repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())})
	if err != nil {
		t.Fatal(err)
	}
	retry := auditHTTPRequest(ctx, recovered, http.MethodPost, "/runs", key, "", body)
	if retry.Code != http.StatusAccepted || retry.Header().Get("Location") != "/runs/"+id {
		t.Fatalf("recovered response = %d %s %s, want original run %s", retry.Code, retry.Header(), retry.Body, id)
	}
	changed := strings.Replace(body, "original", "changed", 1)
	if conflict := auditHTTPRequest(ctx, recovered, http.MethodPost, "/runs", key, "", changed); conflict.Code != http.StatusConflict {
		t.Fatalf("changed retry = %d %s", conflict.Code, conflict.Body)
	}
	var runs, tasks, queued, audits, pending int
	err = db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM scheduled_runs),
		(SELECT count(*) FROM mq_tasks),
		(SELECT count(*) FROM mq_tasks WHERE id = $1 AND status = 'queued'),
		(SELECT count(*) FROM scheduler_audit WHERE operation = 'run.submit'),
		(SELECT pending_runs FROM scheduler_tenant_usage WHERE tenant_id = 'tenant')`, id).
		Scan(&runs, &tasks, &queued, &audits, &pending)
	if err != nil || runs != 1 || tasks != 1 || queued != 1 || audits != 1 || pending != 1 {
		t.Fatalf("runs=%d tasks=%d queued=%d audits=%d pending=%d: %v", runs, tasks, queued, audits, pending, err)
	}
}

func TestPostgresBatchRecoversLostCommitResponse(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	disrupted, fault := commitDroppingDB(t, db)
	const body = `{"runs":[{"idempotency_key":"first","topic":"demo.record","payload":{}},{"idempotency_key":"second","topic":"demo.record","payload":{}}]}`
	send := func(target *sql.DB, want int) []string {
		t.Helper()
		app, err := newApp("127.0.0.1:0", "test-token", "tenant", repository{target, mqstore.NewSQL(target, mqstore.DefaultSQLConfig())})
		if err != nil {
			t.Fatal(err)
		}
		w := auditHTTPRequest(ctx, app, http.MethodPost, "/runs/batch", "", "", body)
		if w.Code != want {
			t.Fatalf("batch status=%d body=%s want=%d", w.Code, w.Body, want)
		}
		if want != http.StatusAccepted {
			return nil
		}
		var result struct {
			Data map[string][]string `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Data["ids"]
	}
	send(disrupted, http.StatusServiceUnavailable)
	select {
	case err := <-fault.dropped:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("batch did not reach PostgreSQL commit fault")
	}
	var committed [2]string
	err := db.QueryRowContext(ctx, `SELECT
		(SELECT id FROM scheduled_runs WHERE request_key = 'first'),
		(SELECT id FROM scheduled_runs WHERE request_key = 'second')`).Scan(&committed[0], &committed[1])
	if err != nil {
		t.Fatal(err)
	}
	if results := send(db, http.StatusAccepted); !slices.Equal(results, committed[:]) {
		t.Fatalf("batch retry got %v, committed %v", results, committed)
	}
	var queued int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM mq_tasks").Scan(&queued); err != nil || queued != 2 {
		t.Fatalf("batch retry queued=%d err=%v", queued, err)
	}
}
