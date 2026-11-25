package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

type batchSubmission struct {
	submission
	Key string `json:"idempotency_key"`
}

func (h api) submitRunBatch(w http.ResponseWriter, req *http.Request) {
	var input struct {
		Runs []batchSubmission `json:"runs"`
	}
	if !decodeRequest(w, req, &input) {
		return
	}
	for _, run := range input.Runs {
		if !h.requireTaskRuntime(w, req, run.Topic) {
			return
		}
	}
	ids, err := h.repo.submitBatch(req.Context(), h.tenant, input.Runs)
	if writeSubmissionError(w, req, err) {
		return
	}
	_ = contract.WriteResponse(w, req, http.StatusAccepted, map[string][]string{"ids": ids}, nil)
}

func (r repository) submitBatch(ctx context.Context, tenant string, batch []batchSubmission) ([]string, error) {
	if len(batch) == 0 || len(batch) > mqstore.SQLBatchLimit {
		return nil, errInvalidTask
	}
	commands := make([]runCommand, len(batch))
	keys := make([]string, len(batch))
	index := make(map[string]int, len(batch))
	for i, item := range batch {
		command, err := prepareSubmission(tenant, item.Key, item.submission)
		if err != nil {
			return nil, err
		}
		if _, exists := index[item.Key]; exists {
			return nil, errInvalidTask
		}
		commands[i], keys[i], index[item.Key] = command, item.Key, i
	}
	tx, err := r.beginAuditedTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var now time.Time
	if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return nil, err
	}
	ids := make([]string, len(batch))
	hashes := make([][]byte, len(batch))
	scheduled := make([]time.Time, len(batch))
	for i := range commands {
		command := &commands[i]
		ids[i], hashes[i], scheduled[i] = rand.Text(), command.hash[:], command.input.RunAt
		if scheduled[i].IsZero() {
			scheduled[i] = now
		}
	}
	rows, err := tx.QueryContext(ctx, `INSERT INTO scheduled_runs (id, tenant_id, request_key, request_hash, scheduled_at)
		SELECT v.id, $1, v.key, v.fingerprint, v.at
		FROM unnest($2::text[], $3::text[], $4::bytea[], $5::timestamptz[]) AS v(id, key, fingerprint, at)
		ORDER BY v.key COLLATE "C"
		ON CONFLICT (tenant_id, request_key) DO NOTHING RETURNING request_key`, tenant, ids, keys, hashes, scheduled)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tasks := make([]mq.Task, 0, len(batch))
	var high int
	source := sourceFromRequest(ctx)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		i := index[key]
		task, err := queuedTask(ids[i], tenant, commands[i].input, scheduled[i], now)
		if err != nil {
			return nil, err
		}
		source.attach(task.Meta, "origin")
		tasks = append(tasks, task)
		if task.Priority == mq.PriorityHigh {
			high++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(tasks) != len(batch) {
		// 使用下一条语句的快照读取与并发提交冲突时已提交的行。
		prior, err := tx.QueryContext(ctx, `SELECT request_key, id, request_hash FROM scheduled_runs
			WHERE tenant_id = $1 AND request_key = ANY($2::text[])`, tenant, keys)
		if err != nil {
			return nil, err
		}
		defer func() { _ = prior.Close() }()
		matched := 0
		for prior.Next() {
			var key, id string
			var hash []byte
			if err := prior.Scan(&key, &id, &hash); err != nil {
				return nil, err
			}
			i := index[key]
			if subtle.ConstantTimeCompare(hashes[i], hash) != 1 {
				return nil, errRequestConflict
			}
			ids[i] = id
			matched++
		}
		if err := prior.Err(); err != nil {
			return nil, err
		}
		if matched != len(batch) {
			return nil, errRequestConflict
		}
	}
	if err := reserveTenantIngress(ctx, tx, tenant, len(tasks)-high, high); err != nil {
		return nil, err
	}
	if err := r.queue.InsertBatchTx(ctx, tx, tasks); err != nil {
		return nil, admissionError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}
