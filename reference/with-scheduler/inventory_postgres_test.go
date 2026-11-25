package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresInventoryEffectAtomicReplayAndProjection(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	receiver, _ := postgresTestDatabases(t)
	ctx := t.Context()
	if _, err := receiver.ExecContext(ctx, receiverDDL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO scheduler_inventory VALUES ('stock-tenant','sku-1',20);
		ALTER TABLE scheduler_inventory ADD CONSTRAINT inject_stock_failure CHECK (available=20)`); err != nil {
		t.Fatal(err)
	}
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	makeTask := func(key, action string, quantity int) mq.Task {
		t.Helper()
		payload, err := json.Marshal(inventoryCommand{"order-1", action, "sku-1", quantity})
		if err != nil {
			t.Fatal(err)
		}
		id, err := repo.submit(ctx, "stock-tenant", key, submission{Topic: inventoryTaskType, Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		rows, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "inventory-test", Limit: 1})
		if err != nil || len(rows) != 1 || rows[0].ID != id {
			t.Fatalf("reserve %s: %+v %v", key, rows, err)
		}
		return rows[0]
	}

	first := makeTask("original", "reserve", 6)
	if err := repo.applyInventory(ctx, first); err == nil {
		t.Fatal("stock update fault did not fail")
	}
	for _, query := range []string{
		"SELECT 1 FROM scheduled_results WHERE run_id=$1", "SELECT 1 FROM scheduled_outbox WHERE run_id=$1",
		"SELECT 1 FROM scheduler_inventory_effects WHERE run_id=$1",
	} {
		var one int
		if err := db.QueryRowContext(ctx, query, first.ID).Scan(&one); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("rollback %s: %v", query, err)
		}
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE scheduler_inventory DROP CONSTRAINT inject_stock_failure;
		UPDATE mq_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE status='leased'`); err != nil {
		t.Fatal(err)
	}
	replacement, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "takeover", Limit: 1})
	if err != nil || len(replacement) != 1 || replacement[0].ID != first.ID {
		t.Fatalf("takeover: %+v %v", replacement, err)
	}
	if err := repo.applyInventory(ctx, first); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("stale owner applied a stock change: %v", err)
	}
	if err := repo.applyInventory(ctx, replacement[0]); err != nil {
		t.Fatal(err)
	}
	if err := repo.applyInventory(ctx, replacement[0]); err != nil {
		t.Fatal(err)
	}
	replay := makeTask("different-request-key", "reserve", 6)
	if err := repo.applyInventory(ctx, replay); err != nil {
		t.Fatal(err)
	}
	conflict := makeTask("conflicting-request-key", "reserve", 3)
	var failure *mq.TaskFailure
	if err := repo.applyInventory(ctx, conflict); !errors.As(err, &failure) || failure.Kind != mq.TaskFailurePermanent {
		t.Fatalf("changed effect payload: %v", err)
	}
	release := makeTask("release", "release", 6)
	if err := repo.applyInventory(ctx, release); err != nil {
		t.Fatal(err)
	}
	var available, effects, outbox int
	if err := db.QueryRowContext(ctx, `SELECT available, (SELECT count(*) FROM scheduler_inventory_effects),
		(SELECT count(*) FROM scheduled_outbox WHERE inventory_effect IS NOT NULL)
		FROM scheduler_inventory WHERE tenant_id='stock-tenant' AND sku='sku-1'`).Scan(&available, &effects, &outbox); err != nil ||
		available != 20 || effects != 2 || outbox != 2 {
		t.Fatalf("inventory/outbox: available=%d effects=%d outbox=%d err=%v", available, effects, outbox, err)
	}

	lost := errors.New("receiver replied after link disconnected")
	_, err = repo.deliverHTTP(ctx, localSchedulerRegion, func(_ context.Context, event deliveryEvent) error {
		if event.ID != first.ID || event.Inventory == nil {
			t.Fatalf("first inventory event: %+v", event)
		}
		if err := receiveOutboxEvent(ctx, receiver, event); err != nil {
			return err
		}
		return lost
	})
	if !errors.Is(err, lost) {
		t.Fatalf("lost response: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE scheduled_outbox SET available_at=clock_timestamp()"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		id, err := repo.deliverHTTP(ctx, localSchedulerRegion, func(_ context.Context, event deliveryEvent) error {
			return receiveOutboxEvent(ctx, receiver, event)
		})
		if err != nil || id == "" {
			t.Fatalf("delivery: %q %v", id, err)
		}
	}
	var reserved, released int
	if err := receiver.QueryRowContext(ctx, `SELECT reserved,released,(SELECT count(*) FROM scheduler_projected_inventory_effects)
		FROM scheduler_projected_inventory WHERE tenant_id='stock-tenant' AND sku='sku-1'`).
		Scan(&reserved, &released, &effects); err != nil || reserved != 6 || released != 6 || effects != 2 {
		t.Fatalf("receiver projection: reserved=%d released=%d effects=%d err=%v", reserved, released, effects, err)
	}
	event := deliveryEvent{ID: first.ID, Inventory: &inventoryEffect{
		Tenant: "stock-tenant", inventoryCommand: inventoryCommand{"order-1", "reserve", "sku-1", 4},
	}}
	if err := receiveOutboxEvent(ctx, receiver, event); !errors.Is(err, errInventoryEffectConflict) {
		t.Fatalf("mismatched receiver replay: %v", err)
	}
	if err := queue.Ack(ctx, replacement[0].ID, replacement[0].LeaseOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := queue.Ack(ctx, release.ID, release.LeaseOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	page, err := reconcileInventory(ctx, db, receiver, "stock-tenant", effectKey{}, 1, time.Minute)
	if err != nil || page.Counts["matched"] != 1 || page.Next == nil {
		t.Fatalf("reconcile first page: %+v %v", page, err)
	}
	page, err = reconcileInventory(ctx, db, receiver, "stock-tenant", *page.Next, 1, time.Minute)
	if err != nil || page.Counts["matched"] != 1 || page.Next != nil {
		t.Fatalf("reconcile final page: %+v %v", page, err)
	}
	unrestored, _ := postgresTestDatabases(t)
	if _, err := unrestored.ExecContext(ctx, receiverDDL); err != nil {
		t.Fatal(err)
	}
	page, err = reconcileInventory(ctx, db, unrestored, "stock-tenant", effectKey{}, 10, time.Minute)
	if err != nil || page.Counts["receiver_missing"] != 2 {
		t.Fatalf("reconcile missing effects: %+v %v", page, err)
	}
	cmd := inventoryRedrive{"stock-tenant", first.ID, "redrive-order", "stock-operator"}
	if err := redriveInventory(ctx, db, receiver, cmd, time.Microsecond); !errors.Is(err, errInventoryRedriveBlocked) {
		t.Fatalf("redrive already recorded downstream effect: %v", err)
	}
	if err := redriveInventory(ctx, db, unrestored, cmd, time.Microsecond); err != nil {
		t.Fatal(err)
	}
	id, err := repo.deliverHTTP(ctx, localSchedulerRegion, func(_ context.Context, ev deliveryEvent) error {
		return receiveOutboxEvent(ctx, unrestored, ev)
	})
	if err != nil || id != first.ID {
		t.Fatalf("redrive changed the event identity: %q %v", id, err)
	}
	if err := redriveInventory(ctx, db, unrestored, cmd, time.Microsecond); err != nil {
		t.Fatalf("idempotent resumed operator command: %v", err)
	}
	cmd.run = release.ID
	if err := redriveInventory(ctx, db, unrestored, cmd, time.Microsecond); !errors.Is(err, errInventoryRedriveConflict) {
		t.Fatalf("operator command key reuse: %v", err)
	}
	cmd.commandKey = "second-redrive"
	if err := redriveInventory(ctx, db, unrestored, cmd, time.Microsecond); !errors.Is(err, errInventoryRedrivePaced) {
		t.Fatalf("unpaced operator command: %v", err)
	}
}
