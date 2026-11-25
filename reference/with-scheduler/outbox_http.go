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
);
CREATE TABLE IF NOT EXISTS scheduler_received_inventory (
	event_id text PRIMARY KEY REFERENCES scheduler_received_events (event_id),
	effect jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS scheduler_projected_inventory_effects (
	tenant_id text NOT NULL,
	operation_id text NOT NULL,
	action text NOT NULL CHECK (action IN ('reserve','release')),
	sku text NOT NULL,
	quantity integer NOT NULL CHECK (quantity > 0),
	committed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
	PRIMARY KEY (tenant_id, operation_id, action)
);
CREATE TABLE IF NOT EXISTS scheduler_projected_inventory (
	tenant_id text NOT NULL,
	sku text NOT NULL,
	reserved bigint NOT NULL DEFAULT 0,
	released bigint NOT NULL DEFAULT 0,
	PRIMARY KEY (tenant_id, sku)
)`

type deliveryEvent struct {
	ID        string           `json:"id"`
	Inventory *inventoryEffect `json:"inventory,omitempty"`
}

type outboxSender func(context.Context, deliveryEvent) error

func referenceEndpoint(endpoint, token string) (string, error) {
	dest, err := url.Parse(endpoint)
	if err != nil || dest.Hostname() == "" || dest.User != nil || dest.Fragment != "" || dest.RawQuery != "" ||
		strings.TrimSpace(token) == "" {
		return "", errors.New("invalid reference destination or token")
	}
	ip := net.ParseIP(dest.Hostname())
	if dest.Scheme != "https" && (dest.Scheme != "http" || ip == nil || !ip.IsLoopback()) {
		return "", errors.New("reference destination requires HTTPS or a loopback IP")
	}
	return dest.String(), nil
}

func newOutboxSender(endpoint, token string) (outboxSender, error) {
	dest, err := referenceEndpoint(endpoint, token)
	if err != nil {
		return nil, err
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
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest, bytes.NewReader(body))
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
	var inventory []byte
	err := r.db.QueryRowContext(ctx, `WITH candidate AS (
		SELECT o.run_id,s.region_epoch FROM scheduled_outbox o`+outboxShardJoin+`
		WHERE s.region=$2 AND o.delivered_at IS NULL AND o.available_at <= clock_timestamp()
		  AND (o.lease_until IS NULL OR o.lease_until <= clock_timestamp())
		ORDER BY o.available_at, o.created_at, o.run_id LIMIT 1
		FOR UPDATE OF o SKIP LOCKED FOR SHARE OF s SKIP LOCKED
	)
	UPDATE scheduled_outbox AS o
	SET lease_token = $1, lease_until = clock_timestamp() + interval '10 seconds'
	FROM candidate WHERE o.run_id = candidate.run_id RETURNING o.run_id,candidate.region_epoch,o.inventory_effect`,
		token, region).Scan(&event.ID, &epoch, &inventory)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if inventory != nil {
		var effect inventoryEffect
		if json.Unmarshal(inventory, &effect) != nil || !effect.valid() {
			return "", errors.New("invalid durable inventory event")
		}
		event.Inventory = &effect
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
		if !runCursorID.MatchString(event.ID) || event.ID != req.Header.Get("Idempotency-Key") ||
			(event.Inventory != nil && !event.Inventory.valid()) {
			writeError(w, req, contract.TypeBadRequest, "invalid event idempotency key")
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		err := receiveOutboxEvent(ctx, db, event)
		if errors.Is(err, errInventoryEffectConflict) {
			writeError(w, req, contract.TypeConflict, "inventory effect conflicts with its receipt")
			return
		}
		if err != nil {
			writeError(w, req, contract.TypeUnavailable, "event persistence unavailable")
			return
		}
		_ = contract.WriteResponse(w, req, http.StatusNoContent, nil, nil)
	})))
	return app, err
}

var errInventoryEffectConflict = errors.New("inventory effect conflicts with its receipt")

func receiveOutboxEvent(ctx context.Context, db *sql.DB, event deliveryEvent) error {
	const receipt = `INSERT INTO scheduler_received_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING`
	if event.Inventory == nil {
		_, err := db.ExecContext(ctx, receipt, event.ID)
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, receipt, event.ID)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(event.Inventory)
	if err != nil {
		return err
	}
	if inserted == 0 {
		var matches bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM scheduler_received_inventory
			WHERE event_id=$1 AND effect=$2::jsonb)`, event.ID, string(encoded)).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return errInventoryEffectConflict
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO scheduler_received_inventory (event_id,effect) VALUES ($1,$2)",
		event.ID, string(encoded)); err != nil {
		return err
	}
	e := event.Inventory
	var sku string
	err = tx.QueryRowContext(ctx, `WITH applied AS (
		INSERT INTO scheduler_projected_inventory_effects (tenant_id,operation_id,action,sku,quantity)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (tenant_id,operation_id,action) DO NOTHING RETURNING sku
	) INSERT INTO scheduler_projected_inventory (tenant_id,sku,reserved,released)
	SELECT $1,sku,CASE WHEN $3='reserve' THEN $5 ELSE 0 END,CASE WHEN $3='release' THEN $5 ELSE 0 END FROM applied
	ON CONFLICT (tenant_id,sku) DO UPDATE SET
		reserved=scheduler_projected_inventory.reserved+excluded.reserved,
		released=scheduler_projected_inventory.released+excluded.released
	RETURNING sku`, e.Tenant, e.OperationID, e.Action, e.SKU, e.Quantity).Scan(&sku)
	if errors.Is(err, sql.ErrNoRows) {
		var matches bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM scheduler_projected_inventory_effects
			WHERE tenant_id=$1 AND operation_id=$2 AND action=$3 AND sku=$4 AND quantity=$5)`,
			e.Tenant, e.OperationID, e.Action, e.SKU, e.Quantity).Scan(&matches); err != nil {
			return err
		}
		if !matches {
			return errInventoryEffectConflict
		}
	} else if err != nil {
		return err
	}
	return tx.Commit()
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
	if err := db.QueryRowContext(probe, `SELECT to_regclass('scheduler_received_events') IS NOT NULL
		AND to_regclass('scheduler_received_inventory') IS NOT NULL
		AND to_regclass('scheduler_projected_inventory_effects') IS NOT NULL
		AND to_regclass('scheduler_projected_inventory') IS NOT NULL`).Scan(&ready); err != nil || !ready {
		return errors.New("receiver schema unavailable")
	}
	app, err := newReceiver(address, os.Getenv("SCHEDULER_OUTBOX_TOKEN"), db)
	if err != nil {
		return err
	}
	return app.RunContext(ctx, 10*time.Second)
}
