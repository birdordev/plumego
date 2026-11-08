package db

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
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
