package messaging

import (
	"testing"
	"time"

	"github.com/spcent/plumego/x/messaging/mq/store"
)

// TestEndToEnd_ReceiptTracking verifies that receipts are created on enqueue
// and updated after worker delivery.
func TestEndToEnd_ReceiptTracking(t *testing.T) {
	sms := &mockSMS{}
	receipts := NewMemReceiptStore(100)

	svc := New(Config{
		TaskStore:         store.NewMemory(store.DefaultMemConfig()),
		SMS:               sms,
		Receipts:          receipts,
		WorkerConcurrency: 2,
	})
	ctx := t.Context()
	_ = svc.Start(ctx)
	defer svc.Stop(ctx)

	err := svc.Send(ctx, SendRequest{
		ID:      "rt-1",
		Channel: ChannelSMS,
		To:      "+1234567890",
		Body:    "receipt test",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify receipt created as queued.
	r, ok := receipts.Get("rt-1")
	if !ok {
		t.Fatal("receipt not found immediately after send")
	}
	if r.Status != messageStatusQueued {
		t.Fatalf("status=%s, want %s", r.Status, messageStatusQueued)
	}

	// Wait for worker to process and persist the final receipt status.
	if !waitForReceiptStatus(receipts, "rt-1", messageStatusSent, 5*time.Second) {
		t.Fatal("timed out waiting for sent receipt")
	}

	// Verify receipt updated to sent.
	r, ok = receipts.Get("rt-1")
	if !ok {
		t.Fatal("receipt not found after delivery")
	}
	if r.Status != messageStatusSent {
		t.Fatalf("status=%s, want %s", r.Status, messageStatusSent)
	}
	if r.ProviderID == "" {
		t.Fatal("provider_id should be set")
	}
	if r.SentAt.IsZero() {
		t.Fatal("sent_at should be set")
	}
}

// TestEndToEnd_MonitorUpdatedByWorker verifies that the channel monitor
// reflects success/failure after worker processing.
func TestEndToEnd_MonitorUpdatedByWorker(t *testing.T) {
	sms := &mockSMS{}
	svc := New(Config{
		TaskStore:         store.NewMemory(store.DefaultMemConfig()),
		SMS:               sms,
		WorkerConcurrency: 1,
	})
	ctx := t.Context()
	_ = svc.Start(ctx)
	defer svc.Stop(ctx)

	_ = svc.Send(ctx, SendRequest{
		ID:      "mon-1",
		Channel: ChannelSMS,
		To:      "+1234567890",
		Body:    "monitor test",
	})

	if !waitForSent(svc, 1, 5*time.Second) {
		t.Fatal("timed out")
	}

	if svc.Monitor().ChannelHealth(ChannelSMS) != ChannelHealthy {
		t.Fatal("expected healthy after successful send")
	}
}

// TestEndToEnd_FailoverProvider verifies failover wiring end to end.
func TestEndToEnd_FailoverProvider(t *testing.T) {
	primary := &failingSMS{}
	backup := &mockSMS{}
	fo := NewFailoverSMSProvider(primary, backup)

	svc := New(Config{
		TaskStore:         store.NewMemory(store.DefaultMemConfig()),
		SMS:               fo,
		WorkerConcurrency: 1,
	})
	ctx := t.Context()
	_ = svc.Start(ctx)
	defer svc.Stop(ctx)

	err := svc.Send(ctx, SendRequest{
		ID:      "fo-1",
		Channel: ChannelSMS,
		To:      "+1234567890",
		Body:    "failover test",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !waitForSent(svc, 1, 5*time.Second) {
		t.Fatal("timed out waiting for failover delivery")
	}

	backup.mu.Lock()
	if len(backup.calls) != 1 {
		t.Fatalf("backup calls=%d, want 1", len(backup.calls))
	}
	backup.mu.Unlock()
}
