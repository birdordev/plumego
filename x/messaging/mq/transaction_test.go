package mq

import (
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/internal/testutil/wait"
	"github.com/birdordev/plumego/x/messaging/pubsub"
)

func TestTransactionManagerBasic(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableTransactions = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	if broker.txManager == nil {
		t.Fatal("txManager should be initialized when transactions are enabled")
	}

	ctx := t.Context()

	// Publish messages within transaction
	txID := "tx-1"
	msg1 := Message{ID: "msg-1", Data: "data-1"}
	msg2 := Message{ID: "msg-2", Data: "data-2"}

	err = broker.PublishWithTransaction(ctx, "test-topic", msg1, txID)
	if err != nil {
		t.Fatalf("PublishWithTransaction error: %v", err)
	}

	err = broker.PublishWithTransaction(ctx, "test-topic", msg2, txID)
	if err != nil {
		t.Fatalf("PublishWithTransaction error: %v", err)
	}

	// Verify messages are buffered
	active, totalMessages := broker.txManager.stats()
	if active != 1 {
		t.Fatalf("expected 1 active transaction, got %d", active)
	}
	if totalMessages != 2 {
		t.Fatalf("expected 2 buffered messages, got %d", totalMessages)
	}

	// Subscribe to topic
	sub, err := broker.Subscribe(ctx, "test-topic", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Messages should not be delivered yet
	select {
	case <-sub.C():
		t.Fatal("messages should not be delivered before commit")
	case <-time.After(100 * time.Millisecond):
		// Expected
	}

	// Commit transaction
	err = broker.CommitTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("CommitTransaction error: %v", err)
	}

	// Verify transaction is cleared
	active, totalMessages = broker.txManager.stats()
	if active != 0 {
		t.Fatalf("expected 0 active transactions after commit, got %d", active)
	}
	if totalMessages != 0 {
		t.Fatalf("expected 0 buffered messages after commit, got %d", totalMessages)
	}

	// Verify messages are delivered
	received := 0
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case <-sub.C():
			received++
			if received == 2 {
				return
			}
		case <-timeout:
			t.Fatalf("timeout waiting for messages, received %d out of 2", received)
		}
	}
}

func TestTransactionRollback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableTransactions = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Subscribe to topic
	sub, err := broker.Subscribe(ctx, "test-topic", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	defer sub.Cancel()

	// Publish messages within transaction
	txID := "tx-rollback"
	msg1 := Message{ID: "msg-1", Data: "data-1"}
	msg2 := Message{ID: "msg-2", Data: "data-2"}

	err = broker.PublishWithTransaction(ctx, "test-topic", msg1, txID)
	if err != nil {
		t.Fatalf("PublishWithTransaction error: %v", err)
	}

	err = broker.PublishWithTransaction(ctx, "test-topic", msg2, txID)
	if err != nil {
		t.Fatalf("PublishWithTransaction error: %v", err)
	}

	// Rollback transaction
	err = broker.RollbackTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("RollbackTransaction error: %v", err)
	}

	// Verify transaction is cleared
	active, totalMessages := broker.txManager.stats()
	if active != 0 {
		t.Fatalf("expected 0 active transactions after rollback, got %d", active)
	}
	if totalMessages != 0 {
		t.Fatalf("expected 0 buffered messages after rollback, got %d", totalMessages)
	}

	// Verify no messages are delivered
	select {
	case <-sub.C():
		t.Fatal("messages should not be delivered after rollback")
	case <-time.After(200 * time.Millisecond):
		// Expected
	}
}

func TestTransactionTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableTransactions = true
	cfg.TransactionTimeout = 100 * time.Millisecond
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Publish message within transaction
	txID := "tx-timeout"
	msg := Message{ID: "msg-1", Data: "data-1"}

	err = broker.PublishWithTransaction(ctx, "test-topic", msg, txID)
	if err != nil {
		t.Fatalf("PublishWithTransaction error: %v", err)
	}

	// Poll for the transaction timeout (100ms) to clear the transaction
	// instead of sleeping a fixed duration.
	wait.ForValue(t, 5*time.Second, "timed-out transaction cleanup", func() int {
		active, _ := broker.txManager.stats()
		return active
	}, 0)

	// Verify transaction is cleared after timeout
	active, totalMessages := broker.txManager.stats()
	if active != 0 {
		t.Fatalf("expected 0 active transactions after timeout, got %d", active)
	}
	if totalMessages != 0 {
		t.Fatalf("expected 0 buffered messages after timeout, got %d", totalMessages)
	}

	// Try to commit after timeout
	err = broker.CommitTransaction(ctx, txID)
	if err == nil {
		t.Fatal("expected error when committing timed-out transaction")
	}
	if !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("expected ErrTransactionNotFound, got %v", err)
	}
}

func TestTransactionNotFound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableTransactions = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Try to commit non-existent transaction
	err = broker.CommitTransaction(ctx, "non-existent")
	if err == nil {
		t.Fatal("expected error for non-existent transaction")
	}
	if !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("expected ErrTransactionNotFound, got %v", err)
	}

	// Try to rollback non-existent transaction
	err = broker.RollbackTransaction(ctx, "non-existent")
	if err == nil {
		t.Fatal("expected error for non-existent transaction")
	}
	if !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("expected ErrTransactionNotFound, got %v", err)
	}
}

func TestTransactionDoubleCommit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableTransactions = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Publish message within transaction
	txID := "tx-double-commit"
	msg := Message{ID: "msg-1", Data: "data-1"}

	err = broker.PublishWithTransaction(ctx, "test-topic", msg, txID)
	if err != nil {
		t.Fatalf("PublishWithTransaction error: %v", err)
	}

	// First commit
	err = broker.CommitTransaction(ctx, txID)
	if err != nil {
		t.Fatalf("CommitTransaction error: %v", err)
	}

	// Try to commit again
	err = broker.CommitTransaction(ctx, txID)
	if err == nil {
		t.Fatal("expected error when committing already committed transaction")
	}
	if !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("expected ErrTransactionNotFound, got %v", err)
	}
}

func TestTransactionDisabled(t *testing.T) {
	// Create broker without transaction support
	broker, err := NewInProcBroker(pubsub.New())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Try to publish with transaction when disabled
	msg := Message{ID: "msg-1", Data: "data-1"}
	err = broker.PublishWithTransaction(ctx, "test-topic", msg, "tx-1")
	if err == nil {
		t.Fatal("expected error when transactions are disabled")
	}
	if !errors.Is(err, ErrTransactionNotSupported) {
		t.Fatalf("expected ErrTransactionNotSupported, got %v", err)
	}

	// Try to commit when disabled
	err = broker.CommitTransaction(ctx, "tx-1")
	if err == nil {
		t.Fatal("expected error when transactions are disabled")
	}
	if !errors.Is(err, ErrTransactionNotSupported) {
		t.Fatalf("expected ErrTransactionNotSupported, got %v", err)
	}

	// Try to rollback when disabled
	err = broker.RollbackTransaction(ctx, "tx-1")
	if err == nil {
		t.Fatal("expected error when transactions are disabled")
	}
	if !errors.Is(err, ErrTransactionNotSupported) {
		t.Fatalf("expected ErrTransactionNotSupported, got %v", err)
	}
}

func TestTransactionConcurrent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableTransactions = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = broker.Close() }()

	ctx := t.Context()

	// Create multiple concurrent transactions
	const numTxs = 10
	const msgsPerTx = 5

	done := make(chan error, numTxs)

	for i := 0; i < numTxs; i++ {
		txID := string(rune('a' + i))
		go func(id string) {
			// Publish multiple messages in transaction
			for j := 0; j < msgsPerTx; j++ {
				msg := Message{
					ID:   id + "-" + string(rune('0'+j)),
					Data: "data",
				}
				if err := broker.PublishWithTransaction(ctx, "test-topic", msg, id); err != nil {
					done <- err
					return
				}
			}
			// Commit transaction
			if err := broker.CommitTransaction(ctx, id); err != nil {
				done <- err
				return
			}
			done <- nil
		}(txID)
	}

	// Wait for all transactions
	for i := 0; i < numTxs; i++ {
		if err := <-done; err != nil {
			t.Fatalf("transaction error: %v", err)
		}
	}

	// Verify all transactions are cleared
	active, totalMessages := broker.txManager.stats()
	if active != 0 {
		t.Fatalf("expected 0 active transactions, got %d", active)
	}
	if totalMessages != 0 {
		t.Fatalf("expected 0 buffered messages, got %d", totalMessages)
	}
}

func TestTransactionClose(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableTransactions = true
	broker, err := NewInProcBroker(pubsub.New(), WithConfig(cfg))
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	// Publish messages within transaction
	txID := "tx-close"
	msg := Message{ID: "msg-1", Data: "data-1"}

	err = broker.PublishWithTransaction(ctx, "test-topic", msg, txID)
	if err != nil {
		t.Fatalf("PublishWithTransaction error: %v", err)
	}

	// Close broker
	_ = broker.Close()

	// Try to add message after close
	err = broker.PublishWithTransaction(ctx, "test-topic", msg, txID)
	if err == nil {
		t.Fatal("expected error after broker close")
	}
}
