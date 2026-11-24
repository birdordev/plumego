package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const schemaVersion = 38

func main() {
	role := flag.String("role", "api", "api, scheduler, worker, finalizer, expiry, outbox, bulk, archive, result-gc, sandbox-gc, autoscale, workflow, events, region-transfer, receiver, receiver-migrate, or migrate")
	address := flag.String("addr", "127.0.0.1:8094", "API listen address")
	migration := flag.String("queue-migration", "../../x/messaging/mq/store/migrations/postgres/001_task_queue.sql", "queue migration file")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *role, *address, *migration); err != nil {
		log.Fatalf("scheduler %s exited: %v", *role, err)
	}
}

func run(ctx context.Context, role, address, migration string) (runErr error) {
	if role == "receiver" || role == "receiver-migrate" {
		return runReceiver(ctx, role, address)
	}
	if role == "sandbox-gc" {
		sandbox, err := newSandboxClient()
		if err != nil {
			return err
		}
		return runSandboxGC(ctx, sandbox)
	}
	dsn := os.Getenv("SCHEDULER_DATABASE_URL")
	if dsn == "" {
		return errors.New("SCHEDULER_DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid PostgreSQL configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	if role == "migrate" {
		if err := migrate(ctx, db, migration); err != nil {
			return errors.New("scheduler migration failed")
		}
		return nil
	}
	if err := checkSchema(ctx, db); err != nil {
		return err
	}

	queueConfig := mqstore.DefaultSQLConfig()
	queueConfig.DeferredExpiration = true
	queueConfig.BlockedClaimsTable = "scheduler_blocked_claims"
	repo := repository{db, mqstore.NewSQL(db, queueConfig)}
	if role == "region-transfer" {
		command, err := loadRegionTransfer()
		if err != nil {
			return err
		}
		transferCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := repo.transferShardRegion(transferCtx, command); err != nil {
			if errors.Is(err, errRegionVersion) {
				return errRegionVersion
			}
			return errors.New("scheduler region transfer failed")
		}
		return nil
	}
	var objects *resultObjects
	if path := os.Getenv("SCHEDULER_RESULT_DIR"); path != "" &&
		(role == "api" || role == "worker" || role == "result-gc") {
		ttl, err := workerDurationEnv("SCHEDULER_RESULT_RETENTION", 24*time.Hour)
		if err != nil {
			return err
		}
		root, err := os.OpenRoot(path)
		if err != nil {
			return errors.New("scheduler result directory unavailable")
		}
		defer func() { _ = root.Close() }()
		objects = &resultObjects{root: root, ttl: ttl}
	}
	var workerPool workerPoolConfig
	var sandbox *sandboxExecutor
	if role == "worker" {
		workerPool, err = loadWorkerPool(os.Getenv("SCHEDULER_WORKER_POOL"), os.Getenv("SCHEDULER_WORKER_TASK_VERSIONS"))
		if err != nil {
			return err
		}
		if objects != nil && workerPool.name == "io" {
			contract, _ := lookupTaskContract(exportTaskType, 1)
			workerPool.contracts = append(workerPool.contracts, contract)
		}
		db.SetMaxOpenConns(workerPool.maxOpen)
		db.SetMaxIdleConns(workerPool.maxIdle)
	}
	if role == "worker" && workerPool.name == "sandbox" {
		sandbox, err = loadSandboxExecutor(ctx)
		if err != nil {
			return err
		}
	}
	var metrics *schedulerMetrics
	if metricsAddress := os.Getenv("SCHEDULER_METRICS_ADDR"); metricsAddress != "" &&
		(role == "scheduler" || role == "worker" || role == "finalizer") {
		metrics = &schedulerMetrics{}
		if role == "scheduler" {
			metrics.readSLO = repo.readSLO
		}
		if role == "worker" {
			metrics.worker = &workerPoolMetrics{name: workerPool.name, capacity: workerPool.concurrency, db: db}
		}
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", metrics)
		metricsCtx, cancel := context.WithCancel(ctx)
		if role != "worker" {
			mux.HandleFunc("GET /readyz", readinessHandler(db, func() bool { return metricsCtx.Err() == nil }))
		}
		closeMetrics, listenerErrors, err := startSchedulerHTTP(metricsAddress, mux)
		if err != nil {
			cancel()
			return err
		}
		ctx = metricsCtx
		watchDone := make(chan struct{})
		var listenerErr error
		go func() {
			defer close(watchDone)
			select {
			case listenerErr = <-listenerErrors:
				cancel()
			case <-metricsCtx.Done():
			}
		}()
		defer func() {
			cancel()
			<-watchDone
			runErr = errors.Join(runErr, listenerErr, closeMetrics())
		}()
	}
	region := localSchedulerRegion
	if role == "scheduler" || role == "outbox" {
		if configured := os.Getenv("SCHEDULER_REGION"); configured != "" {
			region = configured
		}
		if !runCursorID.MatchString(region) {
			return errors.New("invalid SCHEDULER_REGION")
		}
	}
	switch role {
	case "api":
		tracing, err := newSchedulerTracing(os.Getenv("SCHEDULER_TRACE_LOG") == "1")
		if err != nil {
			return err
		}
		app, err := newTracedApp(address, os.Getenv("SCHEDULER_API_TOKEN"), os.Getenv("SCHEDULER_TENANT"), os.Getenv("SCHEDULER_API_ACTOR"), repo, tracing, objects)
		if err != nil {
			return err
		}
		return app.RunContext(ctx, 10*time.Second)
	case "scheduler":
		return repo.runShardedScheduler(ctx, metrics, region)
	case "workflow":
		return runBatches(ctx, repo.advanceWorkflow, "workflow dispatch")
	case "events":
		return runBatches(ctx, repo.advanceWorkflowEvents, "event consumption")
	case "finalizer":
		next := repo.reconcileFinalization
		if metrics != nil {
			next = func(ctx context.Context) (string, error) { return repo.reconcileMeasured(ctx, metrics) }
		}
		return runBatches(ctx, next, "queue finalization")
	case "expiry":
		return runBatches(ctx, func(ctx context.Context) (string, error) {
			const batchSize = 128
			now := time.Now()
			queued, err := repo.queue.ExpireBatch(ctx, mqstore.ExpirationOptions{Limit: batchSize, Now: now})
			leased, leaseErr := repo.expireLeasesWithoutFinalization(ctx, batchSize, now)
			if queued+leased > 0 {
				return "expired", errors.Join(err, leaseErr)
			}
			return "", errors.Join(err, leaseErr)
		}, "queue expiry")
	case "result-gc":
		if objects == nil {
			return errors.New("SCHEDULER_RESULT_DIR is required")
		}
		return runBatches(ctx, func(ctx context.Context) (string, error) {
			return repo.expireResultObject(ctx, objects)
		}, "result object expiry")
	case "archive":
		policy, err := loadRetentionPolicy()
		if err != nil {
			return err
		}
		return runBatches(ctx, func(ctx context.Context) (string, error) {
			return repo.archiveHistory(ctx, policy)
		}, "history retention")
	case "bulk":
		return runBatches(ctx, repo.processBulkCancel, "bulk cancellation")
	case "outbox":
		if endpoint := os.Getenv("SCHEDULER_OUTBOX_URL"); endpoint != "" {
			send, err := newOutboxSender(endpoint, os.Getenv("SCHEDULER_OUTBOX_TOKEN"))
			if err != nil {
				return err
			}
			return runBatches(ctx, func(ctx context.Context) (string, error) {
				return repo.deliverHTTP(ctx, region, send)
			}, "outbox HTTP delivery")
		}
		return runBatches(ctx, func(ctx context.Context) (string, error) {
			return repo.deliverNext(ctx, region)
		}, "outbox delivery")
	case "autoscale":
		return runLocalAutoscaler(ctx, repo)
	case "worker":
		queueConfig.PriorityAging = 30 * time.Second
		if raw := os.Getenv("SCHEDULER_WORKER_PRIORITY_AGING"); raw != "" {
			aging, err := time.ParseDuration(raw)
			if err != nil || aging < 0 {
				return errors.New("invalid SCHEDULER_WORKER_PRIORITY_AGING")
			}
			queueConfig.PriorityAging = aging
		}
		repo.queue = mqstore.NewSQL(db, queueConfig)
		drainTimeout, err := workerDurationEnv("SCHEDULER_WORKER_DRAIN_TIMEOUT", 10*time.Second)
		if err != nil {
			return err
		}
		leaseDuration, err := workerDurationEnv("SCHEDULER_WORKER_LEASE_DURATION", mq.DefaultLeaseDuration)
		if err != nil {
			return err
		}
		taskTimeout, err := workerDurationEnv("SCHEDULER_WORKER_TASK_TIMEOUT", time.Minute)
		if err != nil {
			return err
		}
		tracing, err := newSchedulerTracing(os.Getenv("SCHEDULER_TRACE_LOG") == "1")
		if err != nil {
			return err
		}
		listenerConfig, err := pgx.ParseConfig(dsn)
		if err != nil {
			return errors.New("invalid queue listener configuration")
		}
		var accepting atomic.Bool
		stopProbe, probeDone, err := startWorkerProbe(os.Getenv("SCHEDULER_WORKER_PROBE_ADDR"), db, func() bool {
			return accepting.Load() && ctx.Err() == nil
		})
		if err != nil {
			return err
		}
		wake := make(chan struct{}, 1)
		listenerCtx, stopListener := context.WithCancel(ctx)
		listenerDone := make(chan struct{})
		go func() {
			defer close(listenerDone)
			listenQueueNotifications(listenerCtx, listenerConfig, wake)
		}()
		defer func() {
			stopListener()
			<-listenerDone
		}()
		ackCtx, stopAck := context.WithCancel(context.WithoutCancel(ctx))
		ackQueue, ackDone := startBatchedQueue(ackCtx, repo.queue, 2*time.Millisecond, 16)
		defer func() {
			stopAck()
			<-ackDone
		}()
		w := mq.NewWorker(mq.NewTaskQueue(&journalQueue{TaskStore: ackQueue, repo: repo, metrics: metrics}), mq.WorkerConfig{
			Concurrency:         workerPool.concurrency,
			LeaseDuration:       leaseDuration,
			TaskTimeout:         taskTimeout,
			ShutdownTimeout:     max(30*time.Second, drainTimeout),
			Pool:                workerPool.name,
			Version:             "1",
			PollInterval:        200 * time.Millisecond,
			MaxIdlePollInterval: 2 * time.Second,
			Wake:                wake,
			Deduper:             resultDeduper{db},
			BeginExecution:      repo.beginExecution,
			DedupeKeyFunc: func(task mq.Task) string {
				return task.ID
			},
			FinalizationErrorHook: func(_ context.Context, runID, operation string, _ error) {
				log.Printf("worker %s confirmation unavailable run=%q", operation, runID)
			},
		})
		for _, taskContract := range workerPool.contracts {
			w.Register(taskContract.topic, metrics.handler(tracing.handler(taskContract.handle(repo.workerHandler(taskContract, objects, sandbox)))))
		}
		w.Start(context.WithoutCancel(ctx))
		accepting.Store(true)
		identity := w.Identity()
		log.Printf("worker started instance=%s pool=%s version=%s", identity.InstanceID, identity.Pool, identity.Version)
		var probeErr error
		select {
		case <-ctx.Done():
		case probeErr = <-probeDone:
		}
		accepting.Store(false)
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), drainTimeout)
		defer cancel()
		stopErr := w.Stop(stopCtx)
		if stopErr != nil {
			stopErr = errors.New("worker did not drain within the shutdown budget")
		}
		return errors.Join(stopErr, probeErr, stopProbe())
	default:
		return errors.New("invalid scheduler role")
	}
}

func checkSchema(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var compatible bool
	if err := db.QueryRowContext(ctx, `SELECT count(*) = 1 AND COALESCE(max(version) = $1, false)
		FROM scheduler_schema`, schemaVersion).Scan(&compatible); err != nil || !compatible {
		return errors.New("scheduler schema unavailable or incompatible")
	}
	return nil
}

func migrate(ctx context.Context, db *sql.DB, queueMigration string) error {
	script, err := os.ReadFile(queueMigration)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(7926430081)"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS scheduler_schema (version integer PRIMARY KEY)"); err != nil {
		return err
	}
	var version int
	err = tx.QueryRowContext(ctx, "SELECT version FROM scheduler_schema").Scan(&version)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		for _, query := range []string{string(script), schedulerSchema} {
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO scheduler_schema (version) VALUES (1)"); err != nil {
			return err
		}
	case err != nil:
		return err
	case version == schemaVersion:
		return tx.Commit()
	case version < 1 || version > schemaVersion:
		return errors.New("unrecognized scheduler schema version")
	}
	for _, migration := range []struct {
		version int
		script  string
	}{{2, schedulerSchemaV2}, {3, schedulerSchemaV3}, {4, schedulerSchemaV4}, {5, schedulerSchemaV5}, {6, schedulerSchemaV6}, {7, schedulerSchemaV7}, {8, schedulerSchemaV8}, {9, schedulerSchemaV9}, {10, schedulerSchemaV10}, {11, schedulerSchemaV11}, {12, schedulerSchemaV12}, {13, schedulerSchemaV13}, {14, schedulerSchemaV14}, {15, schedulerSchemaV15}, {16, schedulerSchemaV16}, {17, schedulerSchemaV17}, {18, schedulerSchemaV18}, {19, schedulerSchemaV19}, {20, schedulerSchemaV20}, {21, schedulerSchemaV21}, {22, schedulerSchemaV22}, {23, schedulerSchemaV23}, {24, schedulerSchemaV24}, {25, schedulerSchemaV25}, {26, schedulerSchemaV26}, {27, schedulerSchemaV27}, {28, schedulerSchemaV28}, {29, schedulerSchemaV29}, {30, schedulerSchemaV30}, {31, schedulerSchemaV31}, {32, schedulerSchemaV32}, {33, schedulerSchemaV33}, {34, schedulerSchemaV34}, {35, schedulerSchemaV35}, {36, schedulerSchemaV36}, {37, schedulerSchemaV37}, {38, schedulerSchemaV38}} {
		if version >= migration.version {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.script); err != nil {
			return err
		}
		if migration.version == 22 {
			if err := pinExistingScheduleZones(ctx, tx); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE scheduler_schema SET version = $1", schemaVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func runBatches(ctx context.Context, next func(context.Context) (string, error), operation string) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		for range 64 {
			opCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			id, err := next(opCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Printf("%s failed, will retry", operation)
			}
			if err != nil || id == "" {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
