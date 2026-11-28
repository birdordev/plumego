package operations

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/migrations"
)

func readOperationQueueSchema(t *testing.T) string {
	t.Helper()
	script, err := migrations.ReadFile("queue.sql")
	if err != nil {
		t.Fatal(err)
	}
	return script
}

func TestAcceptanceOperationWorkerTenantClaims(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, readOperationQueueSchema(t)); err != nil {
		t.Fatal(err)
	}
	if err := MigrateWorkerTenantIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_operation_tasks(id,topic,tenant_id,priority,status,attempts,max_attempts,
		available_at,created_at,updated_at,lease_owner,lease_until) VALUES
		('hot-active',$1,'hot',100,'queued',0,8,now()-interval '1 day',now()-interval '1 day',now(),NULL,NULL),
		('hot-expired',$1,'hot',0,'queued',0,8,now()-interval '1 minute',now()-interval '1 minute',now(),NULL,NULL),
		('quiet-expired',$1,'quiet',0,'queued',0,8,now()-interval '1 minute',now()-interval '1 minute',now(),NULL,NULL),
		('quiet-recovery',$1,'quiet',0,'leased',1,8,now()-interval '1 minute',now()-interval '1 minute',now(),'previous',now()-interval '1 second'),
		('quiet-future',$1,'quiet',0,'queued',0,8,now()+interval '1 minute',now(),now(),NULL,NULL)`, topic)
	exec(`UPDATE cmp_operation_tasks SET expires_at=now()-interval '1 second' WHERE id IN ('hot-expired','quiet-expired')`)
	store := NewStore(db)
	quiet := store.WithWorkerTenant("quiet")
	stamp, err := store.dbTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	claim := mq.ReserveOptions{Topics: []string{topic}, Limit: 1, Now: stamp, Lease: time.Minute}
	var workers sync.WaitGroup
	type result struct {
		tasks []mq.Task
		err   error
	}
	const replicas = 6
	results := make(chan result, replicas)
	for i := range replicas {
		workers.Go(func() {
			options := claim
			options.ConsumerID = fmt.Sprintf("quiet-worker-%d", i)
			tasks, err := quiet.reserveWorkerTask(ctx, options)
			results <- result{tasks: tasks, err: err}
		})
	}
	workers.Wait()
	close(results)
	var taken []mq.Task
	for res := range results {
		if res.err != nil {
			t.Fatal(res.err)
		}
		taken = append(taken, res.tasks...)
	}
	if len(taken) != 1 || taken[0].ID != "quiet-recovery" || taken[0].TenantID != "quiet" || taken[0].Attempts != 2 {
		t.Fatalf("incorrect scoped recovery lease: %+v", taken)
	}
	assertStatus := func(id, want string) {
		t.Helper()
		var state string
		if err := db.QueryRowContext(ctx, `SELECT status FROM cmp_operation_tasks WHERE id=$1`, id).Scan(&state); err != nil || state != want {
			t.Fatalf("task %s state=%q want=%q error=%v", id, state, want, err)
		}
	}
	assertStatus("quiet-expired", "expired")
	assertStatus("hot-expired", "queued")
	assertStatus("hot-active", "queued")
	assertStatus("quiet-future", "queued")

	// 共享 Worker 沿用全队列的领取和超期清理路径。
	claim.ConsumerID = "shared-worker"
	tasks, err := store.reserveWorkerTask(ctx, claim)
	if err != nil || len(tasks) != 1 || tasks[0].ID != "hot-active" {
		t.Fatalf("shared worker did not claim old task: %+v, %v", tasks, err)
	}
	assertStatus("hot-expired", "expired")
}
