package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"
)

const (
	shardLease       = 3 * time.Second
	shardHeartbeat   = 500 * time.Millisecond
	shardPoll        = 200 * time.Millisecond
	shardBatchBudget = 64
)

type shardClaim struct {
	owner       string
	shard       int
	regionEpoch int64
}

func (r repository) heartbeatShards(ctx context.Context, owner, region string) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduler_shard_members (id, lease_until, region)
		VALUES ($1, clock_timestamp() + $2 * interval '1 millisecond', $3)
		ON CONFLICT (id) DO UPDATE SET lease_until = EXCLUDED.lease_until WHERE scheduler_shard_members.region = EXCLUDED.region`, owner, shardLease.Milliseconds(), region); err != nil {
		return err
	}
	var previousMembers string
	var previousPending bool
	err = tx.QueryRowContext(ctx, `SELECT live_members, transfers_pending
		FROM scheduler_shard_control FOR UPDATE SKIP LOCKED`).Scan(&previousMembers, &previousPending)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM scheduler_shard_members WHERE lease_until <= statement_timestamp()"); err != nil {
		return err
	}
	var members string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(string_agg(id, ':' ORDER BY id), '')
		FROM scheduler_shard_members WHERE lease_until > statement_timestamp()`).Scan(&members); err != nil {
		return err
	}
	if members == previousMembers && !previousPending {
		return tx.Commit()
	}
	// 最高散列获胜。变更只锁需要转移的分片，并跳过正在提交的分片。
	if _, err := tx.ExecContext(ctx, `WITH live AS (
		SELECT id, region FROM scheduler_shard_members WHERE id = ANY($1::text[])
	), desired AS (
		SELECT s.shard, (SELECT id FROM live WHERE live.region = s.region
			ORDER BY md5(id || ':' || s.shard::text) DESC, id LIMIT 1) AS owner
		FROM scheduler_shards s
	), changed AS MATERIALIZED (
		SELECT s.shard, d.owner FROM scheduler_shards s JOIN desired d USING (shard)
		WHERE s.owner_id IS DISTINCT FROM d.owner
	), transferable AS (
		SELECT s.shard, c.owner FROM scheduler_shards s JOIN changed c USING (shard) FOR UPDATE OF s SKIP LOCKED
	), transferred AS (
		UPDATE scheduler_shards s SET owner_id = t.owner FROM transferable t WHERE t.shard = s.shard RETURNING s.shard
	)
	UPDATE scheduler_shard_control SET live_members = $2,
		transfers_pending = (SELECT count(*) FROM changed) <> (SELECT count(*) FROM transferred)`,
		strings.FieldsFunc(members, func(r rune) bool { return r == ':' }), members); err != nil {
		return err
	}
	return tx.Commit()
}

func (claim shardClaim) lock(ctx context.Context, tx *sql.Tx) (bool, error) {
	var owned int
	err := tx.QueryRowContext(ctx, `SELECT s.shard FROM scheduler_shards s
		JOIN scheduler_shard_members m ON m.id = s.owner_id
		WHERE s.shard = $1 AND s.owner_id = $2 AND s.region_epoch = $3 AND m.region = s.region
		  AND m.lease_until > clock_timestamp()
		FOR SHARE OF s SKIP LOCKED`, claim.shard, claim.owner, claim.regionEpoch).Scan(&owned)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r repository) dueShards(ctx context.Context, owner string) ([]shardClaim, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT s.shard, s.region_epoch FROM scheduler_shards s
		JOIN scheduler_shard_members m ON m.id = s.owner_id
		WHERE m.id = $1 AND m.region = s.region AND m.lease_until > statement_timestamp() AND (
			EXISTS (SELECT 1 FROM schedules WHERE schedule_shard = s.shard AND `+dueScheduleFilter+`)
			OR EXISTS (SELECT 1 FROM scheduled_runs WHERE schedule_shard = s.shard
				AND delay_completed_at IS NOT NULL AND NOT delay_applied))
		ORDER BY s.shard`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var shards []shardClaim
	for rows.Next() {
		claim := shardClaim{owner: owner}
		if err := rows.Scan(&claim.shard, &claim.regionEpoch); err != nil {
			return nil, err
		}
		shards = append(shards, claim)
	}
	return shards, rows.Err()
}

func (r repository) runShardedScheduler(ctx context.Context, metrics *schedulerMetrics, region string) error {
	owner := rand.Text()
	heartbeatContext, stopHeartbeat := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(shardHeartbeat)
		defer ticker.Stop()
		for {
			pulseCtx, cancel := context.WithTimeout(heartbeatContext, time.Second)
			err := r.heartbeatShards(pulseCtx, owner, region)
			cancel()
			if err != nil && heartbeatContext.Err() == nil {
				log.Print("scheduler shard heartbeat failed")
			}
			select {
			case <-heartbeatContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() {
		stopHeartbeat()
		<-heartbeatDone
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if _, err := r.db.ExecContext(releaseCtx, "DELETE FROM scheduler_shard_members WHERE id = $1", owner); err != nil {
			log.Print("scheduler shard membership will expire")
		}
	}()
	ticker := time.NewTicker(shardPoll)
	defer ticker.Stop()
	for ctx.Err() == nil {
		r.processDueShards(ctx, metrics, owner)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

func (r repository) processDueShards(ctx context.Context, metrics *schedulerMetrics, owner string) {
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	shards, err := r.dueShards(readCtx, owner)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			log.Print("scheduler shard scan failed")
		}
		return
	}
	// 每轮为剩余分片各提交一个实例，让单个积压分片无法独占整批。
	for budget := shardBatchBudget; len(shards) > 0 && budget > 0 && ctx.Err() == nil; {
		remaining := shards[:0]
		for _, claim := range shards {
			if budget == 0 || ctx.Err() != nil {
				break
			}
			budget--
			opCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			id, err := r.materializeOwned(opCtx, metrics, claim)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Print("schedule materialization failed, will retry")
			}
			if id != "" && err == nil {
				remaining = append(remaining, claim)
			}
		}
		shards = remaining
	}
}
