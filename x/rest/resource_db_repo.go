package rest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/birdordev/plumego/store/db"
)

// SQLBuilder builds SQL queries from QueryParams.
type SQLBuilder struct {
	table      string
	columns    []string
	idColumn   string
	scanFunc   func(*sql.Rows) (any, error)
	insertFunc func(any) ([]any, error)
	updateFunc func(any) ([]any, error)
}

// NewSQLBuilder creates a new SQL query builder for the given table and ID column.
func NewSQLBuilder(table string, idColumn string) *SQLBuilder {
	return &SQLBuilder{
		table:    table,
		idColumn: idColumn,
		columns:  []string{},
	}
}

// WithColumns sets the columns to select.
func (b *SQLBuilder) WithColumns(columns ...string) *SQLBuilder {
	b.columns = columns
	return b
}

// WithScanFunc sets the function to scan rows into a struct.
func (b *SQLBuilder) WithScanFunc(fn func(*sql.Rows) (any, error)) *SQLBuilder {
	b.scanFunc = fn
	return b
}

// WithInsertFunc sets the function to extract insert values from a struct.
func (b *SQLBuilder) WithInsertFunc(fn func(any) ([]any, error)) *SQLBuilder {
	b.insertFunc = fn
	return b
}

// WithUpdateFunc sets the function to extract update values from a struct.
func (b *SQLBuilder) WithUpdateFunc(fn func(any) ([]any, error)) *SQLBuilder {
	b.updateFunc = fn
	return b
}

// BuildSelectQuery builds a SELECT query with pagination, sorting, and filtering.
func (b *SQLBuilder) BuildSelectQuery(params *QueryParams) (string, []any) {
	var query strings.Builder
	var args []any

	query.WriteString("SELECT ")
	if len(b.columns) > 0 {
		query.WriteString(strings.Join(b.columns, ", "))
	} else {
		query.WriteString("*")
	}
	query.WriteString(" FROM ")
	query.WriteString(b.table)

	if len(params.Filters) > 0 || params.Search != "" {
		query.WriteString(" WHERE ")
		var conditions []string

		if params.Search != "" && len(b.columns) > 0 {
			var searchConds []string
			for _, col := range b.columns {
				searchConds = append(searchConds, fmt.Sprintf("%s LIKE ?", col))
				args = append(args, "%"+params.Search+"%")
			}
			conditions = append(conditions, "("+strings.Join(searchConds, " OR ")+")")
		}

		for key, value := range params.Filters {
			conditions = append(conditions, fmt.Sprintf("%s = ?", key))
			args = append(args, value)
		}

		query.WriteString(strings.Join(conditions, " AND "))
	}

	if len(params.Sort) > 0 {
		query.WriteString(" ORDER BY ")
		var orders []string
		for _, sort := range params.Sort {
			direction := "ASC"
			if sort.Desc {
				direction = "DESC"
			}
			orders = append(orders, fmt.Sprintf("%s %s", sort.Field, direction))
		}
		query.WriteString(strings.Join(orders, ", "))
	}

	if params.Limit > 0 {
		query.WriteString(" LIMIT ?")
		args = append(args, params.Limit)
	}
	if params.Offset > 0 {
		query.WriteString(" OFFSET ?")
		args = append(args, params.Offset)
	}

	return query.String(), args
}

// BuildCountQuery builds a COUNT query with filtering.
func (b *SQLBuilder) BuildCountQuery(params *QueryParams) (string, []any) {
	var query strings.Builder
	var args []any

	query.WriteString("SELECT COUNT(*) FROM ")
	query.WriteString(b.table)

	if len(params.Filters) > 0 || params.Search != "" {
		query.WriteString(" WHERE ")
		var conditions []string

		if params.Search != "" && len(b.columns) > 0 {
			var searchConds []string
			for _, col := range b.columns {
				searchConds = append(searchConds, fmt.Sprintf("%s LIKE ?", col))
				args = append(args, "%"+params.Search+"%")
			}
			conditions = append(conditions, "("+strings.Join(searchConds, " OR ")+")")
		}

		for key, value := range params.Filters {
			conditions = append(conditions, fmt.Sprintf("%s = ?", key))
			args = append(args, value)
		}

		query.WriteString(strings.Join(conditions, " AND "))
	}

	return query.String(), args
}

// BuildInsertQuery builds an INSERT query for the configured columns.
func (b *SQLBuilder) BuildInsertQuery() string {
	placeholders := make([]string, len(b.columns))
	for i := range b.columns {
		placeholders[i] = "?"
	}
	return fmt.Sprintf(
		"INSERT INTO %s (%s) VALUES (%s)",
		b.table,
		strings.Join(b.columns, ", "),
		strings.Join(placeholders, ", "),
	)
}

// BuildUpdateQuery builds an UPDATE query for all configured columns.
func (b *SQLBuilder) BuildUpdateQuery() string {
	setClauses := make([]string, len(b.columns))
	for i, col := range b.columns {
		setClauses[i] = fmt.Sprintf("%s = ?", col)
	}
	return fmt.Sprintf(
		"UPDATE %s SET %s WHERE %s = ?",
		b.table,
		strings.Join(setClauses, ", "),
		b.idColumn,
	)
}

// BuildDeleteQuery builds a DELETE query.
func (b *SQLBuilder) BuildDeleteQuery() string {
	return fmt.Sprintf("DELETE FROM %s WHERE %s = ?", b.table, b.idColumn)
}

// BuildFindByIDQuery builds a SELECT query for a single record by ID.
func (b *SQLBuilder) BuildFindByIDQuery() string {
	selectCols := "*"
	if len(b.columns) > 0 {
		selectCols = strings.Join(b.columns, ", ")
	}
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?", selectCols, b.table, b.idColumn)
}

// BuildExistsQuery builds an EXISTS query.
func (b *SQLBuilder) BuildExistsQuery() string {
	return fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE %s = ?)", b.table, b.idColumn)
}

// BaseRepository provides a base implementation of Repository backed by a sql.DB.
type BaseRepository[T any] struct {
	db      db.DB
	builder *SQLBuilder
}

// NewBaseRepository creates a new BaseRepository with the given database and SQL builder.
func NewBaseRepository[T any](database db.DB, builder *SQLBuilder) *BaseRepository[T] {
	return &BaseRepository[T]{db: database, builder: builder}
}

// DB returns the underlying database connection. Use this when you need to run
// custom queries beyond what BaseRepository provides.
func (r *BaseRepository[T]) DB() db.DB { return r.db }

// FindAll retrieves all records with pagination, filtering, and sorting.
func (r *BaseRepository[T]) FindAll(ctx context.Context, params *QueryParams) ([]T, int64, error) {
	total, err := r.Count(ctx, params)
	if err != nil {
		return nil, 0, err
	}

	query, args := r.builder.BuildSelectQuery(params)
	rows, err := db.QueryContext(ctx, r.db, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query records: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []T
	for rows.Next() {
		if r.builder.scanFunc == nil {
			return nil, 0, fmt.Errorf("scan function not configured")
		}
		item, err := r.builder.scanFunc(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan row: %w", err)
		}
		if typedItem, ok := item.(T); ok {
			results = append(results, typedItem)
		} else if typedItem, ok := item.(*T); ok {
			results = append(results, *typedItem)
		} else {
			return nil, 0, fmt.Errorf("scan function returned wrong type")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows iteration error: %w", err)
	}

	return results, total, nil
}

// FindByID retrieves a single record by ID.
func (r *BaseRepository[T]) FindByID(ctx context.Context, id string) (*T, error) {
	rows, err := db.QueryContext(ctx, r.db, r.builder.BuildFindByIDQuery(), id)
	if err != nil {
		return nil, fmt.Errorf("failed to query record: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("failed to query record: %w", err)
		}
		return nil, sql.ErrNoRows
	}

	if r.builder.scanFunc == nil {
		return nil, fmt.Errorf("scan function not configured")
	}
	item, err := r.builder.scanFunc(rows)
	if err != nil {
		return nil, fmt.Errorf("failed to scan row: %w", err)
	}
	if typedItem, ok := item.(T); ok {
		return &typedItem, nil
	} else if typedItem, ok := item.(*T); ok {
		return typedItem, nil
	}
	return nil, fmt.Errorf("scan function returned wrong type")
}

// Create inserts a new record.
func (r *BaseRepository[T]) Create(ctx context.Context, data *T) error {
	if r.builder.insertFunc == nil {
		return fmt.Errorf("insert function not configured")
	}
	values, err := r.builder.insertFunc(data)
	if err != nil {
		return fmt.Errorf("failed to extract insert values: %w", err)
	}
	_, err = db.ExecContext(ctx, r.db, r.builder.BuildInsertQuery(), values...)
	if err != nil {
		return fmt.Errorf("failed to insert record: %w", err)
	}
	return nil
}

// Update updates an existing record.
func (r *BaseRepository[T]) Update(ctx context.Context, id string, data *T) error {
	if r.builder.updateFunc == nil {
		return fmt.Errorf("update function not configured")
	}
	values, err := r.builder.updateFunc(data)
	if err != nil {
		return fmt.Errorf("failed to extract update values: %w", err)
	}
	values = append(values, id)
	result, err := db.ExecContext(ctx, r.db, r.builder.BuildUpdateQuery(), values...)
	if err != nil {
		return fmt.Errorf("failed to update record: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Delete removes a record by ID.
func (r *BaseRepository[T]) Delete(ctx context.Context, id string) error {
	result, err := db.ExecContext(ctx, r.db, r.builder.BuildDeleteQuery(), id)
	if err != nil {
		return fmt.Errorf("failed to delete record: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Count returns the number of records matching the query.
func (r *BaseRepository[T]) Count(ctx context.Context, params *QueryParams) (int64, error) {
	query, args := r.builder.BuildCountQuery(params)
	row, err := db.QueryRowContext(ctx, r.db, query, args...)
	if err != nil {
		return 0, fmt.Errorf("failed to count records: %w", err)
	}
	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count records: %w", err)
	}
	return count, nil
}

// Exists reports whether a record with the given ID exists.
func (r *BaseRepository[T]) Exists(ctx context.Context, id string) (bool, error) {
	row, err := db.QueryRowContext(ctx, r.db, r.builder.BuildExistsQuery(), id)
	if err != nil {
		return false, fmt.Errorf("failed to check existence: %w", err)
	}
	var exists bool
	if err := row.Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to check existence: %w", err)
	}
	return exists, nil
}
