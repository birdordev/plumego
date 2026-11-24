package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/messaging/mq"
)

type checkpointInput struct {
	Shards        int `json:"shards"`
	ItemsPerShard int `json:"items_per_shard"`
	SegmentSize   int `json:"segment_size"`
	OutputVersion int `json:"output_version"`
}

func parseCheckpointInput(payload json.RawMessage) (checkpointInput, error) {
	var input checkpointInput
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil ||
		input.Shards < 1 || input.Shards > 32 || input.ItemsPerShard < 1 || input.ItemsPerShard > 10000 ||
		input.SegmentSize < 1 || input.SegmentSize > 256 || (input.OutputVersion != 1 && input.OutputVersion != 2) {
		return checkpointInput{}, errInvalidTask
	}
	return input, nil
}

type checkpointCursor struct {
	Shard      int `json:"shard"`
	NextOffset int `json:"next_offset"`
	Generation int `json:"generation"`
}

type checkpointStatus struct {
	checkpointInput
	Cursors []checkpointCursor `json:"cursors"`
}

func lockCheckpointClaim(ctx context.Context, tx *sql.Tx, task mq.Task) error {
	var claim int
	err := tx.QueryRowContext(ctx, `SELECT attempts FROM mq_tasks
		WHERE id=$1 AND tenant_id=$2 AND lease_owner=$3 AND attempts=$4 AND status='leased'
			AND lease_until>clock_timestamp() FOR UPDATE`, task.ID, task.TenantID, task.LeaseOwner, task.Attempts).Scan(&claim)
	if errors.Is(err, sql.ErrNoRows) {
		return mq.ErrLeaseLost
	}
	return err
}

func (r repository) initCheckpoints(ctx context.Context, task mq.Task, input checkpointInput) (checkpointStatus, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return checkpointStatus{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockCheckpointClaim(ctx, tx, task); err != nil {
		return checkpointStatus{}, err
	}
	if _, err := tx.ExecContext(ctx, `WITH inserted AS (
		INSERT INTO scheduler_checkpoint_jobs (run_id,shards,items_per_shard,segment_size,output_version)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT (run_id) DO NOTHING RETURNING run_id,shards
	)
	INSERT INTO scheduler_checkpoints (run_id,shard)
	SELECT run_id,ordinal FROM inserted CROSS JOIN LATERAL generate_series(0,shards-1) ordinal`,
		task.ID, input.Shards, input.ItemsPerShard, input.SegmentSize, input.OutputVersion); err != nil {
		return checkpointStatus{}, err
	}
	state, err := readCheckpoints(ctx, tx, task.TenantID, task.ID)
	if err != nil {
		return checkpointStatus{}, err
	}
	if state.checkpointInput != input || len(state.Cursors) != input.Shards {
		return checkpointStatus{}, &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
	}
	return state, tx.Commit()
}

func segmentDigest(version, shard, start, end int) [sha256.Size]byte {
	h := sha256.New()
	var entry [12]byte
	binary.BigEndian.PutUint32(entry[:4], uint32(version))
	binary.BigEndian.PutUint32(entry[4:8], uint32(shard))
	for index := start; index < end; index++ {
		binary.BigEndian.PutUint32(entry[8:], uint32(index))
		_, _ = h.Write(entry[:])
	}
	var digest [sha256.Size]byte
	h.Sum(digest[:0])
	return digest
}

func (r repository) commitCheckpointSegment(ctx context.Context, task mq.Task, version, shard, start, end int, digest [sha256.Size]byte) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockCheckpointClaim(ctx, tx, task); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `WITH advanced AS (
		UPDATE scheduler_checkpoints c SET next_offset=$5,generation=$6 FROM scheduler_checkpoint_jobs j
		WHERE c.run_id=j.run_id AND c.run_id=$1 AND c.shard=$2 AND c.next_offset=$3
			AND c.generation<=$6 AND j.output_version=$4 AND c.next_offset<j.items_per_shard
			AND $5=least(c.next_offset+j.segment_size,j.items_per_shard)
		RETURNING c.run_id,c.shard
	)
	INSERT INTO scheduler_checkpoint_segments (run_id,shard,start_offset,end_offset,output_version,generation,digest)
	SELECT run_id,shard,$3,$5,$4,$6,$7 FROM advanced`,
		task.ID, shard, start, version, end, task.Attempts, digest[:])
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return mq.ErrLeaseLost
	}
	return tx.Commit()
}

func (r repository) checkpointTask(ctx context.Context, task mq.Task) error {
	input, err := parseCheckpointInput(task.Payload)
	if err != nil {
		return &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
	}
	saved, err := r.initCheckpoints(ctx, task, input)
	if err != nil {
		return err
	}
	for _, shard := range saved.Cursors {
		for offset := shard.NextOffset; offset < input.ItemsPerShard; {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := min(offset+input.SegmentSize, input.ItemsPerShard)
			digest := segmentDigest(input.OutputVersion, shard.Shard, offset, end)
			if err := r.commitCheckpointSegment(ctx, task, input.OutputVersion, shard.Shard, offset, end, digest); err != nil {
				return err
			}
			offset = end
		}
	}
	return r.recordResult(ctx, task)
}

func readCheckpoints(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, tenant, run string) (checkpointStatus, error) {
	var state checkpointStatus
	var raw []byte
	err := db.QueryRowContext(ctx, `SELECT j.shards,j.items_per_shard,j.segment_size,j.output_version,
		(SELECT jsonb_agg(jsonb_build_object('shard',c.shard,'next_offset',c.next_offset,'generation',c.generation)
			ORDER BY c.shard) FROM scheduler_checkpoints c WHERE c.run_id=j.run_id)
		FROM scheduler_checkpoint_jobs j JOIN scheduled_runs r ON r.id=j.run_id
		WHERE r.tenant_id=$1 AND r.id=$2`, tenant, run).
		Scan(&state.Shards, &state.ItemsPerShard, &state.SegmentSize, &state.OutputVersion, &raw)
	if err != nil {
		return state, err
	}
	return state, json.Unmarshal(raw, &state.Cursors)
}

func (h api) checkpoints(w http.ResponseWriter, req *http.Request) {
	state, err := readCheckpoints(req.Context(), h.repo.db, h.tenant, req.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, req, contract.TypeNotFound, "run checkpoints not found")
	case err != nil:
		writeError(w, req, contract.TypeUnavailable, "run checkpoints unavailable")
	default:
		_ = contract.WriteResponse(w, req, http.StatusOK, state, nil)
	}
}
