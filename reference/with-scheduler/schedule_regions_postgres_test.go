package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresSchedulerRegionPartitionTakeoverAndFailback(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	primary := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	cfg, err := pgx.ParseConfig(os.Getenv("PLUMEGO_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.QueryRowContext(ctx, "SELECT quote_ident(current_schema())").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	dial := cfg.DialFunc
	var network sync.Mutex
	var disconnected bool
	var connections []net.Conn
	cfg.DialFunc = func(ctx context.Context, networkType, address string) (net.Conn, error) {
		network.Lock()
		defer network.Unlock()
		if disconnected {
			return nil, errors.New("regional database connection severed")
		}
		conn, err := dial(ctx, networkType, address)
		if err == nil {
			connections = append(connections, conn)
		}
		return conn, err
	}
	westDB := stdlib.OpenDB(*cfg)
	defer westDB.Close()
	west := repository{westDB, mqstore.NewSQL(westDB, mqstore.DefaultSQLConfig())}

	const tenant, plan = "tenant", "regional"
	if err := primary.addSchedule(ctx, scheduleDefinition{Tenant: tenant, ID: plan, Expression: "@every 30s",
		TimeZone: "UTC", Topic: recordTaskType, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	first := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Minute)
	var shard int
	if err := db.QueryRowContext(ctx, "UPDATE schedules SET next_fire_at=$1 WHERE id=$2 RETURNING schedule_shard", first, plan).Scan(&shard); err != nil {
		t.Fatal(err)
	}
	pulse := func(repo repository, owner, region string) {
		t.Helper()
		if err := repo.heartbeatShards(ctx, owner, region); err != nil {
			t.Fatal(err)
		}
	}
	pulse(west, "west-scheduler", localSchedulerRegion)
	pulse(primary, "east-scheduler", "east")
	oldClaim := shardClaim{"west-scheduler", shard, 1}
	whenClaimed, err := west.materializeOwned(ctx, nil, oldClaim)
	if err != nil || whenClaimed == "" {
		t.Fatalf("old owner materialization = %q, %v", whenClaimed, err)
	}
	tasks, err := mq.NewTaskQueue(primary.queue).ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "test-worker", Limit: 1})
	if err != nil || len(tasks) != 1 || tasks[0].ID != whenClaimed {
		t.Fatalf("claim = %+v, %v", tasks, err)
	}
	if err := primary.recordResult(ctx, tasks[0]); err != nil {
		t.Fatal(err)
	}
	receiverDB, _ := postgresTestDatabases(t)
	if _, err := receiverDB.ExecContext(ctx, receiverDDL); err != nil {
		t.Fatal(err)
	}
	receiver, err := newReceiver("127.0.0.1:0", "test-token", receiverDB)
	if err != nil {
		t.Fatal(err)
	}
	var deliveries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		deliveries.Add(1)
		receiver.ServeHTTP(w, req)
	}))
	defer server.Close()
	send, err := newOutboxSender(server.URL+"/events", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	partition := func() {
		network.Lock()
		defer network.Unlock()
		disconnected = true
		for _, conn := range connections {
			_ = conn.Close()
		}
		connections = nil
	}
	var cut time.Time
	if id, err := west.deliverHTTP(ctx, localSchedulerRegion, func(ctx context.Context, event deliveryEvent) error {
		if err := send(ctx, event); err != nil {
			return err
		}
		cut = time.Now()
		partition()
		return nil
	}); err == nil || id != "" || cut.IsZero() {
		t.Fatalf("disconnected confirmation = %q, %v", id, err)
	}
	if _, err := west.materializeOwned(ctx, nil, oldClaim); err == nil {
		t.Fatal("old region retained database connectivity across partition")
	}

	transfer := regionTransfer{Shard: shard, Epoch: 1, From: localSchedulerRegion, To: "east", Actor: "test-operator"}
	if err := primary.transferShardRegion(ctx, transfer); err != nil {
		t.Fatal(err)
	}
	if err := primary.transferShardRegion(ctx, transfer); err != nil {
		t.Fatalf("retry transfer: %v", err)
	}
	stale := transfer
	stale.To = "other"
	if err := primary.transferShardRegion(ctx, stale); !errors.Is(err, errRegionVersion) {
		t.Fatalf("stale transfer: %v", err)
	}
	pulse(primary, "east-scheduler", "east")
	eastClaim := shardClaim{"east-scheduler", shard, 2}
	second, err := primary.materializeOwned(ctx, nil, eastClaim)
	if err != nil || second == "" {
		t.Fatalf("east owner materialization = %q, %v", second, err)
	}
	t.Logf("shard materialized in the recovery region after %s", time.Since(cut))
	network.Lock()
	disconnected = false
	network.Unlock()
	pulse(west, "west-scheduler", localSchedulerRegion)
	if id, err := west.materializeOwned(ctx, nil, oldClaim); err != nil || id != "" {
		t.Fatalf("stale owner reconnected and wrote %q: %v", id, err)
	}
	if id, err := west.deliverHTTP(ctx, localSchedulerRegion, send); err != nil || id != "" {
		t.Fatalf("wrong region delivered %q: %v", id, err)
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		id, err := primary.deliverHTTP(ctx, "east", send)
		return id == whenClaimed, err
	})
	var effects int
	if err := receiverDB.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_received_events").Scan(&effects); err != nil || effects != 1 || deliveries.Load() != 2 {
		t.Fatalf("receiver effects = %d, deliveries = %d: %v", effects, deliveries.Load(), err)
	}

	revert := regionTransfer{Shard: shard, Epoch: 2, From: "east", To: localSchedulerRegion, Actor: transfer.Actor}
	if err := primary.transferShardRegion(ctx, revert); err != nil {
		t.Fatal(err)
	}
	pulse(west, "west-scheduler", localSchedulerRegion)
	for _, claim := range []shardClaim{oldClaim, eastClaim} {
		if id, err := primary.materializeOwned(ctx, nil, claim); err != nil || id != "" {
			t.Fatalf("stale generation after failback wrote %q: %v", id, err)
		}
	}
	third, err := west.materializeOwned(ctx, nil, shardClaim{"west-scheduler", shard, 3})
	if err != nil || third == "" {
		t.Fatalf("failback generation wrote %q: %v", third, err)
	}
	for n, id := range []string{whenClaimed, second, third} {
		occurrence, err := primary.plannedOccurrence(ctx, tenant, plan, first.Add(time.Duration(n)*30*time.Second))
		if err != nil || occurrence.RunID != id {
			t.Fatalf("unexpected occurrence %d = %+v: %v", n, occurrence, err)
		}
	}
}
