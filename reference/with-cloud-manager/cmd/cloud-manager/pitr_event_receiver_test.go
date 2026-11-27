package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/webhook"

	"with-cloud-manager/internal/delivery"
)

type pitrEventReceiver struct {
	db          *sql.DB
	server      *httptest.Server
	destination delivery.Destination
}

func startPITREventReceiver(t *testing.T, root string) pitrEventReceiver {
	t.Helper()
	cluster := pitrCluster{data: filepath.Join(root, "receiver"), socket: filepath.Join(root, "c")}
	if err := os.Mkdir(cluster.socket, 0700); err != nil {
		t.Fatal(err)
	}
	pitrCommand(t, "initdb", "-D", cluster.data, "-U", "postgres", "--no-locale", "--auth-local=trust", "--auth-host=reject")
	cluster.configure(t, "\nlisten_addresses = ''\nunix_socket_directories = "+pitrSetting(cluster.socket)+"\n")
	db := cluster.start(t)
	pitrExec(t, db, `CREATE TABLE fixture_effects (
		effect_id text PRIMARY KEY, action text NOT NULL, asset text NOT NULL, status text NOT NULL
	);
	CREATE TABLE fixture_receipts (
		delivery_id uuid PRIMARY KEY, effect_id text NOT NULL REFERENCES fixture_effects,
		body_hash bytea NOT NULL, body bytea NOT NULL
	);`)
	const secret = "fixture-pitr-event-key-with-more-than-32-bytes"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /events", func(w http.ResponseWriter, r *http.Request) {
		verified, err := webhook.VerifyHMAC(r, webhook.HMACConfig{
			Secret: []byte(secret), Header: "X-Webhook-Signature", Prefix: "v1=", MaxBody: 8192,
			SignedFormat: webhook.PayloadTimestampBody, Replay: webhook.HMACReplayConfig{TimestampHeader: "X-Webhook-Timestamp"},
		})
		if err != nil {
			t.Errorf("invalid recovery webhook signature: %v", err)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var event struct {
			ID, Action, Status, Type, Tenant, Project string
			Operation                                 string `json:"operation_id"`
			Effect                                    string `json:"effect_id"`
			Asset                                     string `json:"asset_id"`
		}
		if err := json.Unmarshal(verified.Body, &event); err != nil || event.ID == "" ||
			event.ID != r.Header.Get("X-Webhook-Delivery") || event.Type != r.Header.Get("X-Webhook-Event") ||
			r.Header.Get("X-Webhook-Id") != "pitr-fixture" || event.Tenant != "tenant" || event.Project != "project" ||
			event.Status != "succeeded" || event.Type != "instance.operation.succeeded" || event.Asset != "ins-pitr" ||
			event.Operation == "" || event.Effect != "cmp-op-final-v1:"+event.Operation ||
			(event.Action != "start" && event.Action != "stop") {
			t.Errorf("invalid recovery event identity or scope: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hash := sha256.Sum256(verified.Body)
		err = dbutil.WithTransaction(r.Context(), db, nil, func(tx *sql.Tx) error {
			var received string
			return tx.QueryRowContext(r.Context(), `WITH effect AS (
				INSERT INTO fixture_effects AS e VALUES ($1,$2,$3,$4)
				ON CONFLICT (effect_id) DO UPDATE SET action=EXCLUDED.action
				WHERE (e.action,e.asset,e.status)=(EXCLUDED.action,EXCLUDED.asset,EXCLUDED.status)
				RETURNING effect_id
			)
			INSERT INTO fixture_receipts AS r (delivery_id,effect_id,body_hash,body)
			SELECT $5,effect_id,$6,$7 FROM effect
			ON CONFLICT (delivery_id) DO UPDATE SET body_hash=EXCLUDED.body_hash
			WHERE (r.effect_id,r.body_hash)=(EXCLUDED.effect_id,EXCLUDED.body_hash)
			RETURNING delivery_id`, event.Effect, event.Action, event.Asset, event.Status, event.ID, hash[:], verified.Body).Scan(&received)
		})
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		if err != nil {
			t.Errorf("record recovery webhook: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	destination, err := delivery.NewDestination(delivery.Config{
		ID: "pitr-fixture", Tenant: "tenant", Project: "project", Endpoint: server.URL + "/events", Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	return pitrEventReceiver{db: db, server: server, destination: destination}
}

func (r pitrEventReceiver) receiptManifest(t *testing.T) delivery.ReceiptManifest {
	t.Helper()
	manifest := delivery.ReceiptManifest{Sink: "pitr-fixture", Tenant: "tenant", Project: "project"}
	rows, err := r.db.QueryContext(t.Context(), "SELECT delivery_id::text,body_hash,body FROM fixture_receipts ORDER BY delivery_id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var hash, body []byte
		if err := rows.Scan(&id, &hash, &body); err != nil {
			t.Fatal(err)
		}
		var receipt delivery.TerminalReceipt
		if err := json.Unmarshal(body, &receipt); err != nil {
			t.Fatal(err)
		}
		receipt.DeliveryID, receipt.BodySHA256 = id, hex.EncodeToString(hash)
		manifest.Receipts = append(manifest.Receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func (r pitrEventReceiver) publish(t *testing.T, source *sql.DB) {
	t.Helper()
	publisher, err := delivery.NewPublisher(t.Context(), delivery.NewStore(source), []delivery.Destination{r.destination}, r.server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- publisher.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("recovery publisher stopped with error: %v", err)
			}
		case <-time.After(8 * time.Second):
			t.Error("recovery publisher did not stop")
		}
	}()
	pitrWait(t, "two settled webhook deliveries", func(ctx context.Context) (bool, error) {
		var delivered int
		err := source.QueryRowContext(ctx, "SELECT count(*) FROM cmp_operation_event_outbox WHERE delivered_at IS NOT NULL").Scan(&delivered)
		return delivered == 2, err
	})
}

func (r pitrEventReceiver) assertEffects(t *testing.T, prior, pending string, pendingReceipts int) {
	t.Helper()
	var effects, receipts, priorCount, pendingCount int
	err := r.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM fixture_effects),
		count(*),count(*) FILTER (WHERE effect_id=$1),count(*) FILTER (WHERE effect_id=$2)
		FROM fixture_receipts`, "cmp-op-final-v1:"+prior, "cmp-op-final-v1:"+pending).
		Scan(&effects, &receipts, &priorCount, &pendingCount)
	if err != nil || effects != 2 || receipts != 1+pendingReceipts || priorCount != 1 || pendingCount != pendingReceipts {
		t.Fatalf("receiver effects=%d receipts=%d prior=%d recovered=%d error=%v", effects, receipts, priorCount, pendingCount, err)
	}
}
