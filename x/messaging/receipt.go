package messaging

import (
	"sync"
	"time"
)

// ReceiptStore persists delivery receipts for status queries. Service writes
// receipts on a best-effort basis. A failed Save is reported through the
// configured logger and does not change message acceptance or retry decisions.
type ReceiptStore interface {
	Save(receipt Receipt) error
	Get(id string) (Receipt, bool)
	List(filter ReceiptFilter) []Receipt
}

// Receipt records the outcome of a send attempt.
type Receipt struct {
	ID         string    `json:"id"`
	Channel    Channel   `json:"channel"`
	To         string    `json:"to"`
	Status     string    `json:"status"` // queued, sent, failed, or dead
	ProviderID string    `json:"provider_id,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	Error      string    `json:"error,omitempty"`
	Attempts   int       `json:"attempts"`
	TenantID   string    `json:"tenant_id,omitempty"`
	QueuedAt   time.Time `json:"queued_at"`
	SentAt     time.Time `json:"sent_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ReceiptFilter controls list queries.
type ReceiptFilter struct {
	Channel  Channel `json:"channel,omitempty"`
	Status   string  `json:"status,omitempty"`
	TenantID string  `json:"tenant_id,omitempty"`
	Limit    int     `json:"limit,omitempty"`
	Offset   int     `json:"offset,omitempty"`
}

// MemReceiptStore is a thread-safe in-memory receipt store.
// Suitable for development; replace with SQL-backed implementation for production.
type MemReceiptStore struct {
	mu       sync.RWMutex
	receipts map[string]Receipt
	order    []string // insertion order for List
	maxSize  int
}

// NewMemReceiptStore creates an in-memory store.
// maxSize limits entries; 0 means 10000.
func NewMemReceiptStore(maxSize int) *MemReceiptStore {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &MemReceiptStore{
		receipts: make(map[string]Receipt, 256),
		order:    make([]string, 0, 256),
		maxSize:  maxSize,
	}
}

func (s *MemReceiptStore) Save(r Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.receipts[r.ID]; !exists {
		// Evict oldest if at capacity.
		if len(s.order) >= s.maxSize {
			oldest := s.order[0]
			s.order = s.order[1:]
			delete(s.receipts, oldest)
		}
		s.order = append(s.order, r.ID)
	}
	r.UpdatedAt = time.Now()
	s.receipts[r.ID] = r
	return nil
}

func (s *MemReceiptStore) Get(id string) (Receipt, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.receipts[id]
	return r, ok
}

func (s *MemReceiptStore) List(f ReceiptFilter) []Receipt {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	var result []Receipt
	skipped := 0
	// Iterate in reverse (newest first).
	for i := len(s.order) - 1; i >= 0; i-- {
		r := s.receipts[s.order[i]]
		if f.Channel != "" && r.Channel != f.Channel {
			continue
		}
		if f.Status != "" && r.Status != f.Status {
			continue
		}
		if f.TenantID != "" && r.TenantID != f.TenantID {
			continue
		}
		if skipped < offset {
			skipped++
			continue
		}
		result = append(result, r)
		if len(result) >= limit {
			break
		}
	}
	return result
}
