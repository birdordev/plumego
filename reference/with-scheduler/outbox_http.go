package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/middleware/auth"
	"github.com/birdordev/plumego/security/authn"
)

const receiverDDL = `CREATE TABLE IF NOT EXISTS scheduler_received_events (
	event_id text PRIMARY KEY,
	received_at timestamptz NOT NULL DEFAULT clock_timestamp()
)`

type deliveryEvent struct {
	ID string `json:"id"`
}

type outboxSender func(context.Context, deliveryEvent) error

func newOutboxSender(endpoint, token string) (outboxSender, error) {
	dest, err := url.Parse(endpoint)
	if err != nil || dest.Hostname() == "" || dest.User != nil || dest.Fragment != "" || dest.RawQuery != "" ||
		strings.TrimSpace(token) == "" {
		return nil, errors.New("invalid outbox destination or token")
	}
	ip := net.ParseIP(dest.Hostname())
	if dest.Scheme != "https" && (dest.Scheme != "http" || ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("outbox destination requires HTTPS or a loopback IP")
	}
	client := &http.Client{
		Timeout:       2500 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return func(ctx context.Context, event deliveryEvent) error {
		body, err := json.Marshal(event)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest.String(), bytes.NewReader(body))
		if err != nil {
			return errors.New("invalid outbox request")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", event.ID)
		resp, err := client.Do(req)
		if err != nil {
			return errors.New("outbox receiver unavailable")
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != http.StatusNoContent {
			return errors.New("outbox receiver did not confirm delivery")
		}
		return nil
	}, nil
}

var errOutboxLeaseLost = errors.New("outbox lease lost")

const outboxShardJoin = ` JOIN scheduled_runs r ON r.id=o.run_id
	JOIN scheduler_shards s ON s.shard=COALESCE(r.schedule_shard,scheduler_plan_shard(r.tenant_id,r.id)) `

func (r repository) deliverHTTP(ctx context.Context, region string, send outboxSender) (string, error) {
	token := rand.Text()
	var event deliveryEvent
	var epoch int64
	err := r.db.QueryRowContext(ctx, `WITH candidate AS (
		SELECT o.run_id,s.region_epoch FROM scheduled_outbox o`+outboxShardJoin+`
		WHERE s.region=$2 AND o.delivered_at IS NULL AND o.available_at <= clock_timestamp()
		  AND (o.lease_until IS NULL OR o.lease_until <= clock_timestamp())
		ORDER BY o.available_at, o.created_at, o.run_id LIMIT 1
		FOR UPDATE OF o SKIP LOCKED FOR SHARE OF s SKIP LOCKED
	)
	UPDATE scheduled_outbox AS o
	SET lease_token = $1, lease_until = clock_timestamp() + interval '10 seconds'
	FROM candidate WHERE o.run_id = candidate.run_id RETURNING o.run_id,candidate.region_epoch`,
		token, region).Scan(&event.ID, &epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err := send(ctx, event); err != nil {
		// 调用超时后保留租约，由下一位投递者在过期后接管。
		if ctx.Err() != nil {
			return "", err
		}
		_, releaseErr := r.db.ExecContext(ctx, outboxRegionGuard+`UPDATE scheduled_outbox o
			SET lease_token=NULL, lease_until=NULL, available_at=clock_timestamp()+interval '1 second'
			FROM owned WHERE o.run_id=owned.run_id AND o.lease_token=$2 AND o.delivered_at IS NULL`,
			event.ID, token, region, epoch)
		return "", errors.Join(err, releaseErr)
	}

	result, err := r.db.ExecContext(ctx, outboxRegionGuard+`UPDATE scheduled_outbox o
		SET delivered_at=clock_timestamp(), lease_token=NULL, lease_until=NULL
		FROM owned WHERE o.run_id=owned.run_id AND o.lease_token=$2
		AND o.lease_until>clock_timestamp() AND o.delivered_at IS NULL`, event.ID, token, region, epoch)
	if err != nil {
		return "", err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if rows != 1 {
		return "", errOutboxLeaseLost
	}
	return event.ID, nil
}

const outboxRegionGuard = `WITH owned AS MATERIALIZED (
	SELECT o.run_id FROM scheduled_outbox o` + outboxShardJoin + `
	WHERE o.run_id=$1 AND s.region=$3 AND s.region_epoch=$4 FOR SHARE OF s
) `

func newReceiver(address, token string, db *sql.DB) (*core.App, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("SCHEDULER_OUTBOX_TOKEN is required")
	}
	cfg := core.DefaultConfig()
	cfg.Addr = address
	app := core.New(cfg, core.AppDependencies{})
	authenticate, err := auth.Authenticate(authn.StaticToken(token), auth.WithAuthRealm("scheduler-receiver"))
	if err != nil {
		return nil, err
	}
	err = app.AddRoute(http.MethodPost, "/events", authenticate(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var event deliveryEvent
		if !decodeRequest(w, req, &event) {
			return
		}
		if !runCursorID.MatchString(event.ID) || event.ID != req.Header.Get("Idempotency-Key") {
			writeError(w, req, contract.TypeBadRequest, "invalid event idempotency key")
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, `INSERT INTO scheduler_received_events (event_id)
			VALUES ($1) ON CONFLICT DO NOTHING`, event.ID); err != nil {
			writeError(w, req, contract.TypeUnavailable, "event persistence unavailable")
			return
		}
		_ = contract.WriteResponse(w, req, http.StatusNoContent, nil, nil)
	})))
	return app, err
}

func runReceiver(ctx context.Context, role, address string) error {
	dsn := os.Getenv("SCHEDULER_RECEIVER_DATABASE_URL")
	if dsn == "" {
		return errors.New("SCHEDULER_RECEIVER_DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid receiver database configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(8)
	if role == "receiver-migrate" {
		if _, err := db.ExecContext(ctx, receiverDDL); err != nil {
			return errors.New("receiver migration failed")
		}
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ready bool
	if err := db.QueryRowContext(probe, "SELECT to_regclass('scheduler_received_events') IS NOT NULL").Scan(&ready); err != nil || !ready {
		return errors.New("receiver schema unavailable")
	}
	app, err := newReceiver(address, os.Getenv("SCHEDULER_OUTBOX_TOKEN"), db)
	if err != nil {
		return err
	}
	return app.RunContext(ctx, 10*time.Second)
}
