package cloudtest

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type pitrCluster struct {
	data, socket string
}

const pitrRestorePoint = "cloud_manager_before_power_receipt"

func newPITRClusters(t *testing.T) (primary, backup pitrCluster, archive string) {
	t.Helper()
	for _, tool := range []string{"initdb", "pg_ctl", "pg_basebackup"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("PITR drill requires %s: %v", tool, err)
		}
	}
	root, err := os.MkdirTemp("", "cmp-pitr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	primary = pitrCluster{data: filepath.Join(root, "primary"), socket: filepath.Join(root, "a")}
	backup = pitrCluster{data: filepath.Join(root, "restored"), socket: filepath.Join(root, "b")}
	archive = filepath.Join(root, "archive")
	for _, dir := range []string{primary.socket, backup.socket, archive} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	pitrCommand(t, "initdb", "-D", primary.data, "-U", "postgres", "--no-locale", "--auth-local=trust", "--auth-host=reject")
	primary.configure(t, "\nlisten_addresses = ''\nwal_level = replica\narchive_mode = on\n"+
		"unix_socket_directories = "+pitrSetting(primary.socket)+"\narchive_command = "+
		pitrSetting("cp '%p' "+pitrShellArg(archive)+"/'%f'")+"\n")
	return primary, backup, archive
}

func (c pitrCluster) dsn() string {
	query := url.Values{"host": {c.socket}, "sslmode": {"disable"}, "port": {"5432"}, "connect_timeout": {"2"}}
	return (&url.URL{Scheme: "postgresql", User: url.User("postgres"), Path: "/postgres", RawQuery: query.Encode()}).String()
}

func (c pitrCluster) configure(t *testing.T, settings string) {
	t.Helper()
	config, err := os.OpenFile(filepath.Join(c.data, "postgresql.conf"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = config.WriteString(settings)
	closeErr := config.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("configure PostgreSQL: %v %v", err, closeErr)
	}
}

func (c pitrCluster) start(t *testing.T) *sql.DB {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		if exec.CommandContext(ctx, "pg_ctl", "-D", c.data, "status").Run() == nil {
			if output, err := exec.CommandContext(ctx, "pg_ctl", "-D", c.data, "-m", "fast", "-w", "stop").CombinedOutput(); err != nil {
				t.Errorf("stop test PostgreSQL: %v: %s", err, output)
			}
		}
	})
	logPath := c.data + ".log"
	if err := pitrRun(t.Context(), "pg_ctl", "-D", c.data, "-l", logPath, "-w", "-t", "25", "start"); err != nil {
		log, logErr := os.ReadFile(logPath)
		t.Fatalf("start PostgreSQL: %v\n%s\nread log: %v", err, log, logErr)
	}
	config, err := pgx.ParseConfig(c.dsn())
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

func (c pitrCluster) startRestore(t *testing.T, archive string) *sql.DB {
	t.Helper()
	c.configure(t, "\narchive_mode = off\nunix_socket_directories = "+pitrSetting(c.socket)+
		"\nrestore_command = "+pitrSetting("cp "+pitrShellArg(archive)+"/'%f' '%p'")+
		"\nrecovery_target_name = "+pitrSetting(pitrRestorePoint)+"\nrecovery_target_action = 'promote'\n")
	if err := os.WriteFile(filepath.Join(c.data, "recovery.signal"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	db := c.start(t)
	pitrWait(t, "restored PostgreSQL to promote", func(ctx context.Context) (bool, error) {
		var ready bool
		err := db.QueryRowContext(ctx, "SELECT NOT pg_is_in_recovery()").Scan(&ready)
		return ready, err
	})
	return db
}

func archivePITRTail(t *testing.T, db *sql.DB, markerFile, archive string) {
	t.Helper()
	var tailFile string
	if err := db.QueryRowContext(t.Context(), "SELECT pg_walfile_name(pg_switch_wal())").Scan(&tailFile); err != nil {
		t.Fatal(err)
	}
	pitrWait(t, "WAL to be archived through the post-target request", func(ctx context.Context) (bool, error) {
		var archived bool
		err := db.QueryRowContext(ctx, "SELECT COALESCE(last_archived_wal >= $1,false) FROM pg_stat_archiver", tailFile).Scan(&archived)
		return archived, err
	})
	for _, wal := range []string{markerFile, tailFile} {
		if file, err := os.Stat(filepath.Join(archive, wal)); err != nil || file.Size() == 0 {
			t.Fatalf("missing archived WAL %s: %v", wal, err)
		}
	}
}

func pitrWait(t *testing.T, description string, check func(context.Context) (bool, error)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		ready, err := check(ctx)
		if ready && err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s: %v", description, err)
		case <-tick.C:
		}
	}
}

func pitrCommand(t *testing.T, name string, args ...string) {
	t.Helper()
	if err := pitrRun(t.Context(), name, args...); err != nil {
		t.Fatal(err)
	}
}

func pitrRun(parent context.Context, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, output)
	}
	return nil
}

func pitrSetting(value string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "'", "''") + "'"
}

func pitrShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
