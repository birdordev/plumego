package dbschema

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

const onlineMigrationLockID = migrationLockID + 1

// ApplyOnline 在固定会话持有迁移锁，执行可安全续跑的事务外变更。
func ApplyOnline(ctx context.Context, db *sql.DB, version string, apply func(context.Context, *sql.Conn) error) (retErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			retErr = errors.Join(retErr, err)
		}
	}()
	if err := lockOnlineMigration(ctx, conn); err != nil {
		// 锁回执可能丢失，关闭数据库会话以释放可能已持有的锁。
		discardMigrationConnection(conn)
		return err
	}
	defer func() {
		if err := unlockMigrationConnection(ctx, conn); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	if _, err := conn.ExecContext(ctx, migrationLedger); err != nil {
		return err
	}
	var applied bool
	if err := conn.QueryRowContext(ctx, migrationExists, version).Scan(&applied); err != nil || applied {
		return err
	}
	if err := apply(ctx, conn); err != nil {
		return fmt.Errorf("apply online migration %s: %w", version, err)
	}
	_, err = conn.ExecContext(ctx, recordMigration, version)
	return err
}

func lockOnlineMigration(ctx context.Context, conn *sql.Conn) error {
	retry := time.NewTicker(100 * time.Millisecond)
	defer retry.Stop()
	for {
		var locked bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", onlineMigrationLockID).Scan(&locked); err != nil || locked {
			return err
		}
		// 等待方的每次尝试都结束当前语句，避免持有并发建索引正在等待的快照。
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-retry.C:
		}
	}
}

func unlockMigrationConnection(ctx context.Context, conn *sql.Conn) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var unlocked bool
	err := conn.QueryRowContext(cleanup, "SELECT pg_advisory_unlock($1)", onlineMigrationLockID).Scan(&unlocked)
	if err == nil && unlocked {
		return nil
	}
	discardMigrationConnection(conn)
	return errors.Join(errors.New("release migration session lock"), err)
}

func discardMigrationConnection(conn *sql.Conn) {
	// Raw 以 ErrBadConn 通知连接池销毁会话，已失效的连接无需处理。
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}
