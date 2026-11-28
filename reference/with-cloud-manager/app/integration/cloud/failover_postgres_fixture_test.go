package cloudtest

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const failoverStandbyName = "cloud_manager_failover"

func startCloudFailoverStandby(t *testing.T, primary, standby pitrCluster, source *sql.DB) *sql.DB {
	t.Helper()
	replication := failoverURL(t, primary)
	query := replication.Query()
	query.Set("application_name", failoverStandbyName)
	replication.RawQuery = query.Encode()
	pitrCommand(t, "pg_basebackup", "--dbname", replication.String(), "--pgdata", standby.data,
		"--wal-method=stream", "--checkpoint=fast", "--write-recovery-conf", "--no-password")
	standby.configure(t, "\narchive_mode = off\nunix_socket_directories = "+pitrSetting(standby.socket)+"\n")
	replica := standby.start(t)
	pitrExec(t, source, "ALTER SYSTEM SET synchronous_standby_names TO "+pitrSetting("FIRST 1 ("+failoverStandbyName+")"))
	pitrExec(t, source, "ALTER SYSTEM SET synchronous_commit TO 'remote_apply'")
	pitrExec(t, source, "SELECT pg_reload_conf()")
	pitrWait(t, "synchronous WAL replay on the failover standby", func(ctx context.Context) (bool, error) {
		var ready bool
		err := source.QueryRowContext(ctx, `SELECT current_setting('synchronous_commit')='remote_apply' AND EXISTS (
			SELECT 1 FROM pg_stat_replication WHERE application_name=$1 AND state='streaming'
			AND sync_state='sync' AND replay_lsn IS NOT NULL)`, failoverStandbyName).Scan(&ready)
		return ready, err
	})
	return replica
}

func cloudFailoverWriter(t *testing.T, first, standby pitrCluster) *sql.DB {
	t.Helper()
	endpoint := failoverURL(t, first)
	query := endpoint.Query()
	query.Set("host", first.socket+","+standby.socket)
	query.Set("target_session_attrs", "read-write")
	endpoint.RawQuery = query.Encode()
	config, err := pgx.ParseConfig(endpoint.String())
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func failoverURL(t *testing.T, cluster pitrCluster) *url.URL {
	t.Helper()
	endpoint, err := url.Parse(cluster.dsn())
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func assertCloudPrimaryStopped(t *testing.T, primary pitrCluster) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := exec.CommandContext(ctx, "pg_ctl", "-D", primary.data, "status").Run()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 3 {
		t.Fatalf("old test primary was not fenced: %v", err)
	}
}
