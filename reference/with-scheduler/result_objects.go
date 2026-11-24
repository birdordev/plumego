package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

const maxResultBytes = 8 << 20

type resultObjects struct {
	root *os.Root
	ttl  time.Duration
}

func resultObjectName(tenant, id string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(tenant+"\x00"+id)))
}

func (r repository) publishResultObject(ctx context.Context, objects *resultObjects, task mq.Task, size int, data io.Reader) error {
	if objects == nil || size < 1 || size > maxResultBytes {
		return errors.New("invalid result object configuration")
	}
	id := rand.Text()
	intent, err := r.db.ExecContext(ctx, `INSERT INTO scheduler_result_objects (id, tenant_id, run_id, state, size_bytes, expires_at)
		SELECT $1, q.tenant_id, q.id, 'staging', $4, statement_timestamp() + interval '15 minutes'
		FROM mq_tasks q JOIN scheduled_runs r ON r.id = q.id AND r.tenant_id = q.tenant_id
		WHERE q.tenant_id = $2 AND q.id = $3 AND q.status = 'leased' AND q.lease_owner = $5
			AND q.lease_until > clock_timestamp()`, id, task.TenantID, task.ID, size, task.LeaseOwner)
	if err != nil {
		return err
	}
	rows, err := intent.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return mq.ErrLeaseLost
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockStagedResult(ctx, tx, id); err != nil {
		return err
	}
	f, err := objects.root.OpenFile(resultObjectName(task.TenantID, id), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	// 已打开的 inode 不会在清理后重新出现，流式写入期间可归还连接。
	if err := tx.Commit(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := objects.write(ctx, f, size, data); err != nil {
		return err
	}
	return r.commitResultObject(ctx, objects, task, id)
}

func lockStagedResult(ctx context.Context, tx *sql.Tx, id string) error {
	var staging string
	return tx.QueryRowContext(ctx, `SELECT state FROM scheduler_result_objects WHERE id = $1
		AND state = 'staging' AND expires_at > statement_timestamp() FOR UPDATE`, id).Scan(&staging)
}

func (r repository) commitResultObject(ctx context.Context, objects *resultObjects, task mq.Task, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockStagedResult(ctx, tx, id); err != nil {
		return err
	}
	inserted, err := recordResultInTx(ctx, tx, task, sql.NullString{String: id, Valid: true})
	if err != nil {
		return err
	}
	if inserted {
		if _, err := tx.ExecContext(ctx, `UPDATE scheduler_result_objects SET state = 'ready',
			expires_at = statement_timestamp() + $2 * interval '1 second' WHERE id = $1`, id, objects.ttl.Seconds()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (o *resultObjects) write(ctx context.Context, f *os.File, size int, data io.Reader) error {
	_, err := io.CopyN(f, data, int64(size))
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = f.Sync()
	}
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	return syncObjectDirectory(o.root)
}

func syncObjectDirectory(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func (r repository) expireResultObject(ctx context.Context, objects *resultObjects) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var id, tenant, state string
	err = tx.QueryRowContext(ctx, `SELECT id, tenant_id, state FROM scheduler_result_objects
		WHERE state IN ('staging','ready') AND expires_at <= statement_timestamp()
		ORDER BY expires_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &tenant, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err := objects.root.Remove(resultObjectName(tenant, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := syncObjectDirectory(objects.root); err != nil {
		return "", err
	}
	if state == "staging" {
		_, err = tx.ExecContext(ctx, "DELETE FROM scheduler_result_objects WHERE id = $1", id)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE scheduler_result_objects SET state = 'expired' WHERE id = $1", id)
	}
	if err != nil {
		return "", err
	}
	return id, tx.Commit()
}
