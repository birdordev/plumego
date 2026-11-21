package db

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

type trackedTransaction struct {
	commits   atomic.Int32
	rollbacks atomic.Int32
}

func (tx *trackedTransaction) Commit() error   { tx.commits.Add(1); return nil }
func (tx *trackedTransaction) Rollback() error { tx.rollbacks.Add(1); return nil }

func TestTransactionAlwaysReleasesConnection(t *testing.T) {
	for _, outcome := range []string{"commit", "error", "panic", "goexit"} {
		t.Run(outcome, func(t *testing.T) {
			tracked := &trackedTransaction{}
			database := sql.OpenDB(&stubConnector{conn: &stubConn{tx: tracked}})
			defer database.Close()
			database.SetMaxOpenConns(1)
			callbackErr := errors.New("callback failed")
			finished := make(chan struct{})
			var returned error
			var recovered any
			go func() {
				defer close(finished)
				defer func() { recovered = recover() }()
				returned = WithTransaction(context.Background(), database, nil, func(*sql.Tx) error {
					switch outcome {
					case "error":
						return callbackErr
					case "panic":
						panic("callback panic")
					case "goexit":
						runtime.Goexit()
					}
					return nil
				})
			}()
			<-finished
			if outcome == "commit" {
				if returned != nil || tracked.commits.Load() != 1 || tracked.rollbacks.Load() != 0 {
					t.Fatalf("commit path: error=%v, commits=%d, rollbacks=%d", returned, tracked.commits.Load(), tracked.rollbacks.Load())
				}
			} else if tracked.commits.Load() != 0 || tracked.rollbacks.Load() != 1 {
				t.Fatalf("exit path: commits=%d, rollbacks=%d", tracked.commits.Load(), tracked.rollbacks.Load())
			}
			if outcome == "error" && (!errors.Is(returned, callbackErr) || !errors.Is(returned, ErrTransactionFailed)) {
				t.Fatalf("callback error = %v", returned)
			}
			if outcome == "panic" && recovered != "callback panic" {
				t.Fatalf("panic = %v", recovered)
			}
			if got := database.Stats().InUse; got != 0 {
				t.Fatalf("connections still in use = %d", got)
			}
		})
	}
}

type gatedCommit struct {
	trackedTransaction
	started, proceed chan struct{}
}

func (tx *gatedCommit) Commit() error {
	close(tx.started)
	<-tx.proceed
	return tx.trackedTransaction.Commit()
}

type observedRollback struct {
	trackedTransaction
	finished chan struct{}
}

func (tx *observedRollback) Rollback() error {
	defer close(tx.finished)
	return tx.trackedTransaction.Rollback()
}

func TestCancellationAtTransactionCommitBoundary(t *testing.T) {
	t.Run("before driver commit", func(t *testing.T) {
		tracked := &observedRollback{finished: make(chan struct{})}
		database := sql.OpenDB(&stubConnector{conn: &stubConn{tx: tracked}})
		t.Cleanup(func() { _ = database.Close() })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		err := WithTransaction(ctx, database, nil, func(*sql.Tx) error {
			cancel()
			return nil
		})
		if !errors.Is(err, ErrTransactionFailed) ||
			(!errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone)) {
			t.Fatalf("unexpected result before commit: %v", err)
		}
		select {
		case <-tracked.finished:
		case <-time.After(5 * time.Second):
			t.Fatal("database/sql did not complete cancellation rollback")
		}
		if tracked.commits.Load() != 0 || tracked.rollbacks.Load() != 1 {
			t.Fatalf("commits=%d rollbacks=%d", tracked.commits.Load(), tracked.rollbacks.Load())
		}
	})
	t.Run("after driver commit enters", func(t *testing.T) {
		tracked := &gatedCommit{started: make(chan struct{}), proceed: make(chan struct{})}
		database := sql.OpenDB(&stubConnector{conn: &stubConn{tx: tracked}})
		t.Cleanup(func() { _ = database.Close() })
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- WithTransaction(ctx, database, nil, func(*sql.Tx) error { return nil })
		}()
		<-tracked.started
		cancel()
		close(tracked.proceed)
		if err := <-done; err != nil || tracked.commits.Load() != 1 || tracked.rollbacks.Load() != 0 {
			t.Fatalf("error=%v commits=%d rollbacks=%d", err, tracked.commits.Load(), tracked.rollbacks.Load())
		}
	})
}
