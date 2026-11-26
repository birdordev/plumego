package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"with-cloud-manager/internal/cmdb"
)

const cmdbUsage = "usage: cloud-manager cmdb-migrate|cmdb-sync TENANT PROJECT|cmdb-status TENANT PROJECT"

func runCMDB(ctx context.Context, args []string, getenv func(string) string, output io.Writer) (retErr error) {
	dsn := getenv("CLOUD_MANAGER_CMDB_DATABASE_URL")
	if dsn == "" {
		return errors.New("CLOUD_MANAGER_CMDB_DATABASE_URL is required")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid CMDB PostgreSQL configuration")
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	defer func() {
		if err := db.Close(); err != nil {
			retErr = errors.Join(retErr, errors.New("closing CMDB storage connection failed"))
		}
	}()
	return runCMDBCommand(ctx, db, args, getenv("CLOUD_MANAGER_API_URL"), getenv("CLOUD_MANAGER_API_TOKEN"), output, nil)
}

func runCMDBCommand(parent context.Context, db *sql.DB, args []string, address, token string, output io.Writer, transport http.RoundTripper) error {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	if len(args) == 1 && args[0] == "cmdb-migrate" {
		if err := cmdb.Migrate(ctx, db); err != nil {
			return cmdbCommandError(err)
		}
		return nil
	}
	if len(args) != 3 || !operationClientSegment(args[1]) || !operationClientSegment(args[2]) {
		return errors.New(cmdbUsage)
	}
	base, err := operationClientBase(address)
	if err != nil {
		return err
	}
	if len(base.String()) > 1024 {
		return errors.New("CMDB API base URL exceeds 1024 bytes")
	}
	project := cmdb.Project{Tenant: args[1], Project: args[2]}
	store := cmdb.NewStore(db)
	var receipt cmdb.Receipt
	switch args[0] {
	case "cmdb-sync":
		if !validProjectClientToken(token) {
			return errors.New("a valid CLOUD_MANAGER_API_TOKEN is required")
		}
		receipt, err = store.Sync(ctx, base, project, token, transport)
	case "cmdb-status":
		receipt, err = store.Status(ctx, cmdb.MirrorKey{Source: base.String(), Scope: project})
	default:
		return errors.New(cmdbUsage)
	}
	if err != nil {
		return cmdbCommandError(err)
	}
	return json.NewEncoder(output).Encode(receipt)
}

func cmdbCommandError(err error) error {
	var status cmdb.HTTPStatusError
	if errors.As(err, &status) {
		return status
	}
	for _, public := range []error{cmdb.ErrSourceUnavailable, cmdb.ErrNoReceipt, cmdb.ErrInvalidSnapshot,
		cmdb.ErrSnapshotLimit, cmdb.ErrStaleSnapshot, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, public) {
			return public
		}
	}
	return errors.New("CMDB storage is unavailable")
}
