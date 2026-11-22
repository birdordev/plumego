package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	errIdempotencyConflict = errors.New("idempotency key conflicts with an earlier request")
	errPrecondition        = errors.New("item version changed")
)

const currentSchemaVersion = 2

type item struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Version int64  `json:"version"`
}

type createdItem struct {
	Item   item
	Replay bool
}

type repository struct{ db *sql.DB }

func (r repository) schemaVersion(ctx context.Context) (int, error) {
	var version int
	err := r.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
	return version, err
}

func (r repository) migrate(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == currentSchemaVersion {
		return nil
	}
	if version < 0 || version > currentSchemaVersion {
		return fmt.Errorf("unsupported schema version %d", version)
	}
	if version == 0 {
		for _, statement := range []string{
			`CREATE TABLE items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, version INTEGER NOT NULL)`,
			`CREATE TABLE create_receipts (key TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, item_id INTEGER NOT NULL, name TEXT NOT NULL, expires_at INTEGER NOT NULL)`,
			`CREATE INDEX receipt_expiry ON create_receipts(expires_at)`,
			`CREATE TABLE outbox (id INTEGER PRIMARY KEY AUTOINCREMENT, item_id INTEGER NOT NULL, kind TEXT NOT NULL)`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE delivered_events (
		event_id INTEGER PRIMARY KEY, item_id INTEGER NOT NULL, kind TEXT NOT NULL)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", currentSchemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func (r repository) create(ctx context.Context, key, fingerprint, name string, now time.Time) (createdItem, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return createdItem{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM create_receipts WHERE key = ? AND expires_at <= ?", key, now.Unix()); err != nil {
		return createdItem{}, err
	}
	var saved createdItem
	var storedFingerprint string
	err = tx.QueryRowContext(ctx, "SELECT fingerprint, item_id, name FROM create_receipts WHERE key = ?", key).
		Scan(&storedFingerprint, &saved.Item.ID, &saved.Item.Name)
	if err == nil {
		if storedFingerprint != fingerprint {
			return createdItem{}, errIdempotencyConflict
		}
		saved.Item.Version, saved.Replay = 1, true
		return saved, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return createdItem{}, err
	}
	saved.Item.Name, saved.Item.Version = name, 1
	if err := tx.QueryRowContext(ctx, "INSERT INTO items(name, version) VALUES (?, 1) RETURNING id", name).
		Scan(&saved.Item.ID); err != nil {
		return createdItem{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO create_receipts VALUES (?, ?, ?, ?, ?)",
		key, fingerprint, saved.Item.ID, name, now.Add(24*time.Hour).Unix()); err != nil {
		return createdItem{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO outbox(item_id, kind) VALUES (?, 'item.created')", saved.Item.ID); err != nil {
		return createdItem{}, err
	}
	return saved, tx.Commit()
}

func (r repository) get(ctx context.Context, id int64) (item, error) {
	var result item
	err := r.db.QueryRowContext(ctx, "SELECT id, name, version FROM items WHERE id = ?", id).
		Scan(&result.ID, &result.Name, &result.Version)
	return result, err
}

func (r repository) update(ctx context.Context, id, version int64, name string) (item, error) {
	var result item
	err := r.db.QueryRowContext(ctx, `UPDATE items SET name = ?, version = version + 1
		WHERE id = ? AND (version = ? OR ? = 0) RETURNING id, name, version`, name, id, version, version).
		Scan(&result.ID, &result.Name, &result.Version)
	if errors.Is(err, sql.ErrNoRows) {
		err = errPrecondition
	}
	return result, err
}

func (r repository) delete(ctx context.Context, id, version int64) error {
	var deleted int64
	err := r.db.QueryRowContext(ctx, "DELETE FROM items WHERE id = ? AND (version = ? OR ? = 0) RETURNING id", id, version, version).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return errPrecondition
	}
	return err
}

type outboxEvent struct {
	ID     int64
	ItemID int64
	Kind   string
}

// recordDelivery 将副作用与去重记录合并为一次持久写入。
func (r repository) recordDelivery(ctx context.Context, event outboxEvent) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO delivered_events (event_id, item_id, kind)
		VALUES (?, ?, ?) ON CONFLICT(event_id) DO NOTHING`, event.ID, event.ItemID, event.Kind)
	return err
}

func (r repository) sendNext(ctx context.Context, publish func(context.Context, outboxEvent) error) (bool, error) {
	var event outboxEvent
	err := r.db.QueryRowContext(ctx, "SELECT id, item_id, kind FROM outbox ORDER BY id LIMIT 1").
		Scan(&event.ID, &event.ItemID, &event.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := publish(ctx, event); err != nil {
		return false, err
	}
	_, err = r.db.ExecContext(ctx, "DELETE FROM outbox WHERE id = ?", event.ID)
	return true, err
}
