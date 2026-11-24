package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresOutboxRetriesAfterReceiverCommitAndLostResponse(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	receiverDB, _ := postgresTestDatabases(t)
	ctx := t.Context()
	if _, err := receiverDB.ExecContext(ctx, receiverDDL); err != nil {
		t.Fatal(err)
	}
	receiver, err := newReceiver("127.0.0.1:0", "test-token", receiverDB)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if requests.Add(1) == 1 {
			recorder := httptest.NewRecorder()
			receiver.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusNoContent {
				t.Errorf("first receiver result: %d", recorder.Code)
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		receiver.ServeHTTP(w, req)
	}))
	defer server.Close()

	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "external", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "outbox-test", Limit: 1})
	if err != nil || len(tasks) != 1 || tasks[0].ID != id {
		t.Fatalf("reserve: %+v %v", tasks, err)
	}
	if err := repo.recordResult(ctx, tasks[0]); err != nil {
		t.Fatal(err)
	}
	send, err := newOutboxSender(server.URL+"/events", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repo.deliverHTTP(ctx, localSchedulerRegion, send); err == nil || got != "" {
		t.Fatalf("lost confirmation: %q %v", got, err)
	}
	var receiptCount int
	if err := receiverDB.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_received_events WHERE event_id = $1", id).Scan(&receiptCount); err != nil || receiptCount != 1 {
		t.Fatalf("receiver receipt after lost response: %d %v", receiptCount, err)
	}

	// 模拟数据库确认失败，接收方仍应只持久化一条回执。
	if _, err := db.ExecContext(ctx, `UPDATE scheduled_outbox SET available_at = clock_timestamp();
		ALTER TABLE scheduled_outbox ADD CONSTRAINT inject_confirmation_failure CHECK (delivered_at IS NULL)`); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.deliverHTTP(ctx, localSchedulerRegion, send); err == nil || got != "" {
		t.Fatalf("confirmation failure: %q %v", got, err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE scheduled_outbox DROP CONSTRAINT inject_confirmation_failure;
		UPDATE scheduled_outbox SET lease_until = clock_timestamp() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.deliverHTTP(ctx, localSchedulerRegion, send); err != nil || got != id {
		t.Fatalf("recovered delivery: %q %v", got, err)
	}
	if err := receiverDB.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_received_events").Scan(&receiptCount); err != nil || receiptCount != 1 {
		t.Fatalf("duplicate effect: %d %v", receiptCount, err)
	}
	if requests.Load() != 3 {
		t.Fatalf("receiver requests = %d", requests.Load())
	}
}

func TestPostgresOutboxClaimCanBeReplacedOnlyAfterExpiry(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "lease", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "outbox-test", Limit: 1})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("reserve: %+v %v", tasks, err)
	}
	if err := repo.recordResult(ctx, tasks[0]); err != nil {
		t.Fatal(err)
	}
	send := func(context.Context, deliveryEvent) error { return nil }
	stale := func(ctx context.Context, _ deliveryEvent) error {
		if got, err := repo.deliverHTTP(ctx, localSchedulerRegion, send); got != "" || err != nil {
			t.Fatalf("claimed before expiry: %q %v", got, err)
		}
		if _, err := db.ExecContext(ctx, "UPDATE scheduled_outbox SET lease_until = clock_timestamp() - interval '1 second'"); err != nil {
			t.Fatal(err)
		}
		if got, err := repo.deliverHTTP(ctx, localSchedulerRegion, send); got != id || err != nil {
			t.Fatalf("did not take over expired lease: %q %v", got, err)
		}
		return nil
	}
	if got, err := repo.deliverHTTP(ctx, localSchedulerRegion, stale); got != "" || !errors.Is(err, errOutboxLeaseLost) {
		t.Fatalf("stale completion: %q %v", got, err)
	}
}
