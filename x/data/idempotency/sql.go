package idempotency

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectMySQL    Dialect = "mysql"
)

type SQLConfig struct {
	Dialect        Dialect
	Table          string
	Now            func() time.Time
	DuplicateError func(error) bool
}

func DefaultSQLConfig() SQLConfig {
	return SQLConfig{
		Dialect: DialectPostgres,
		Table:   "idempotency_keys",
		Now:     time.Now,
	}
}

type SQLStore struct {
	db  *sql.DB
	cfg SQLConfig
	now func() time.Time
}

func NewSQLStore(db *sql.DB, cfg SQLConfig) *SQLStore {
	if cfg.Dialect == "" {
		cfg.Dialect = DialectPostgres
	}
	if cfg.Table == "" {
		cfg.Table = "idempotency_keys"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &SQLStore{db: db, cfg: cfg, now: cfg.Now}
}

func (s *SQLStore) Get(ctx context.Context, key string) (Record, bool, error) {
	if s == nil || s.db == nil {
		return Record{}, false, ErrNotFound
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return Record{}, false, ErrInvalidKey
	}

	table, err := s.tableName()
	if err != nil {
		return Record{}, false, err
	}

	query := fmt.Sprintf("SELECT key, request_hash, status, response, created_at, updated_at, expires_at FROM %s WHERE key = %s", table, s.placeholder(1))
	row := s.db.QueryRowContext(ctx, query, key)

	var rec Record
	var status string
	var expiresAt sql.NullTime
	if err := row.Scan(&rec.Key, &rec.RequestHash, &status, &rec.Response, &rec.CreatedAt, &rec.UpdatedAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, false, nil
		}
		return Record{}, false, err
	}
	if expiresAt.Valid {
		rec.ExpiresAt = expiresAt.Time
	}
	rec.Status = Status(status)

	if !rec.ExpiresAt.IsZero() && !rec.ExpiresAt.After(s.now()) {
		_ = s.Delete(ctx, key)
		return Record{}, false, nil
	}

	return rec, true, nil
}

func (s *SQLStore) PutIfAbsent(ctx context.Context, record Record) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrNotFound
	}
	if strings.TrimSpace(record.Key) == "" {
		return false, ErrInvalidKey
	}
	if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(s.now()) {
		return false, ErrExpired
	}

	now := s.now()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if record.Status == "" {
		record.Status = StatusInProgress
	}

	query, args, err := s.buildInsert(record)
	if err != nil {
		return false, err
	}
	_, err = s.db.ExecContext(ctx, query, args...)
	if err != nil {
		if s.isDuplicateError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *SQLStore) Complete(ctx context.Context, key string, response []byte) error {
	if s == nil || s.db == nil {
		return ErrNotFound
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ErrInvalidKey
	}

	now := s.now()
	table, err := s.tableName()
	if err != nil {
		return err
	}

	query := fmt.Sprintf(
		"UPDATE %s SET status = %s, response = %s, updated_at = %s WHERE key = %s AND status = %s AND (expires_at IS NULL OR expires_at > %s)",
		table,
		s.placeholder(1),
		s.placeholder(2),
		s.placeholder(3),
		s.placeholder(4),
		s.placeholder(5),
		s.placeholder(6),
	)
	res, err := s.db.ExecContext(ctx, query, StatusCompleted, response, now, key, string(StatusInProgress), now)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) Delete(ctx context.Context, key string) error {
	if s == nil || s.db == nil {
		return ErrNotFound
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return ErrInvalidKey
	}

	table, err := s.tableName()
	if err != nil {
		return err
	}

	query := fmt.Sprintf("DELETE FROM %s WHERE key = %s", table, s.placeholder(1))
	_, err = s.db.ExecContext(ctx, query, key)
	return err
}

func (s *SQLStore) buildInsert(record Record) (string, []any, error) {
	table, err := s.tableName()
	if err != nil {
		return "", nil, err
	}

	cols := []string{"key", "request_hash", "status", "response", "created_at", "updated_at", "expires_at"}
	placeholders := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	for i := range cols {
		placeholders = append(placeholders, s.placeholder(i+1))
	}
	args = append(args, record.Key, record.RequestHash, string(record.Status), record.Response, record.CreatedAt, record.UpdatedAt, nullTime(record.ExpiresAt))
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(placeholders, ", "))
	return query, args, nil
}

func (s *SQLStore) placeholder(idx int) string {
	if s.cfg.Dialect == DialectPostgres {
		return fmt.Sprintf("$%d", idx)
	}
	return "?"
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return !errors.Is(err, sql.ErrNoRows) && (strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique"))
}

func (s *SQLStore) isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	if s != nil && s.cfg.DuplicateError != nil && s.cfg.DuplicateError(err) {
		return true
	}
	return isDuplicateError(err)
}

func (s *SQLStore) tableName() (string, error) {
	table := strings.TrimSpace(s.cfg.Table)
	if table == "" {
		table = "idempotency_keys"
	}
	if !validSQLIdentifierPath(table) {
		return "", fmt.Errorf("idempotency: invalid table name %s", strconv.Quote(table))
	}
	return table, nil
}

func validSQLIdentifierPath(value string) bool {
	parts := strings.Split(value, ".")
	for _, part := range parts {
		if !validSQLIdentifier(part) {
			return false
		}
	}
	return true
}

func validSQLIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') {
			continue
		}
		if i > 0 && '0' <= r && r <= '9' {
			continue
		}
		return false
	}
	return true
}
