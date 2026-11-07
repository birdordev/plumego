package messaging

import (
	"testing"
	"time"
)

func TestMemReceiptStore_SaveAndGet(t *testing.T) {
	s := NewMemReceiptStore(100)

	r := Receipt{
		ID:       "r1",
		Channel:  ChannelSMS,
		To:       "+1234567890",
		Status:   messageStatusQueued,
		TenantID: "t1",
		QueuedAt: time.Now(),
	}
	if err := s.Save(r); err != nil {
		t.Fatal(err)
	}

	got, ok := s.Get("r1")
	if !ok {
		t.Fatal("receipt not found")
	}
	if got.Channel != ChannelSMS {
		t.Fatalf("channel=%s, want sms", got.Channel)
	}
	if got.Status != messageStatusQueued {
		t.Fatalf("status=%s, want %s", got.Status, messageStatusQueued)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("updated_at should be set")
	}
}

func TestMemReceiptStore_Update(t *testing.T) {
	s := NewMemReceiptStore(100)

	_ = s.Save(Receipt{ID: "u1", Status: messageStatusQueued})
	_ = s.Save(Receipt{ID: "u1", Status: messageStatusSent, ProviderID: "pid-1"})

	got, _ := s.Get("u1")
	if got.Status != messageStatusSent {
		t.Fatalf("status=%s, want %s", got.Status, messageStatusSent)
	}
	if got.ProviderID != "pid-1" {
		t.Fatalf("provider_id=%s, want pid-1", got.ProviderID)
	}
}

func TestMemReceiptStore_Eviction(t *testing.T) {
	s := NewMemReceiptStore(3)

	_ = s.Save(Receipt{ID: "e1"})
	_ = s.Save(Receipt{ID: "e2"})
	_ = s.Save(Receipt{ID: "e3"})
	_ = s.Save(Receipt{ID: "e4"}) // should evict e1

	if _, ok := s.Get("e1"); ok {
		t.Fatal("e1 should have been evicted")
	}
	if _, ok := s.Get("e4"); !ok {
		t.Fatal("e4 should exist")
	}
}

func TestMemReceiptStore_List(t *testing.T) {
	s := NewMemReceiptStore(100)

	_ = s.Save(Receipt{ID: "l1", Channel: ChannelSMS, Status: messageStatusSent})
	_ = s.Save(Receipt{ID: "l2", Channel: ChannelEmail, Status: messageStatusSent})
	_ = s.Save(Receipt{ID: "l3", Channel: ChannelSMS, Status: messageStatusFailed})
	_ = s.Save(Receipt{ID: "l4", Channel: ChannelEmail, Status: messageStatusQueued})

	// Filter by channel.
	smsReceipts := s.List(ReceiptFilter{Channel: ChannelSMS})
	if len(smsReceipts) != 2 {
		t.Fatalf("sms receipts=%d, want 2", len(smsReceipts))
	}

	// Filter by status.
	sentReceipts := s.List(ReceiptFilter{Status: messageStatusSent})
	if len(sentReceipts) != 2 {
		t.Fatalf("sent receipts=%d, want 2", len(sentReceipts))
	}

	// Combined filter.
	smsFailed := s.List(ReceiptFilter{Channel: ChannelSMS, Status: messageStatusFailed})
	if len(smsFailed) != 1 {
		t.Fatalf("sms failed=%d, want 1", len(smsFailed))
	}

	// Newest first.
	all := s.List(ReceiptFilter{})
	if len(all) != 4 {
		t.Fatalf("all=%d, want 4", len(all))
	}
	if all[0].ID != "l4" {
		t.Fatalf("first item=%s, want l4 (newest first)", all[0].ID)
	}
}

func TestMemReceiptStore_ListWithTenant(t *testing.T) {
	s := NewMemReceiptStore(100)

	_ = s.Save(Receipt{ID: "t1", TenantID: "a"})
	_ = s.Save(Receipt{ID: "t2", TenantID: "b"})
	_ = s.Save(Receipt{ID: "t3", TenantID: "a"})

	got := s.List(ReceiptFilter{TenantID: "a"})
	if len(got) != 2 {
		t.Fatalf("tenant a=%d, want 2", len(got))
	}
}

func TestMemReceiptStore_GetNotFound(t *testing.T) {
	s := NewMemReceiptStore(100)
	_, ok := s.Get("nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}
