package session

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/internal/testutil/wait"
	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

type blockedSessionTokenizer struct {
	tokenizer.Tokenizer
	once    sync.Once
	started chan struct{}
	resume  chan struct{}
}

func (b *blockedSessionTokenizer) Count(string) (int, error) {
	b.once.Do(func() {
		close(b.started)
		<-b.resume
	})
	return 1, nil
}

func TestManagerSerializesSessionChanges(t *testing.T) {
	for _, appendSecond := range []bool{true, false} {
		t.Run(fmt.Sprintf("appendSecond=%v", appendSecond), func(t *testing.T) {
			ctx := t.Context()
			counter := &blockedSessionTokenizer{started: make(chan struct{}), resume: make(chan struct{})}
			resume := sync.OnceFunc(func() { close(counter.resume) })
			defer resume()
			m := NewManager(NewMemoryStorage(), WithTokenizer(counter))
			s, err := m.Create(ctx, CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			first, second := make(chan error, 1), make(chan error, 1)
			go func() { first <- m.AppendMessage(ctx, s.ID, provider.Message{Role: provider.RoleUser}) }()
			select {
			case <-counter.started:
			case <-time.After(3 * time.Second):
				t.Fatal("first append did not start counting tokens")
			}
			go func() {
				if appendSecond {
					second <- m.AppendMessage(ctx, s.ID, provider.Message{Role: provider.RoleAssistant})
				} else {
					second <- m.SetContext(ctx, s.ID, "key", "value")
				}
			}()
			wait.ForCondition(t, 3*time.Second, "both session changes registered", func() bool {
				m.sessionMu.Lock()
				defer m.sessionMu.Unlock()
				lock := m.sessions[s.ID]
				return lock != nil && lock.refs == 2
			})
			resume()
			for _, result := range []chan error{first, second} {
				if err := <-result; err != nil {
					t.Fatal(err)
				}
			}
			got, err := m.Get(ctx, s.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantMessages := 1
			if appendSecond {
				wantMessages = 2
				if len(got.Messages) == 2 && got.Messages[1].Role != provider.RoleAssistant {
					t.Errorf("messages = %v, second message should be the assistant", got.Messages)
				}
			} else if got.Context["key"] != "value" {
				t.Errorf("context = %v, want key=value", got.Context)
			}
			if len(got.Messages) != wantMessages || got.Usage.TotalTokens != wantMessages {
				t.Errorf("messages = %d, tokens = %d, want %d each", len(got.Messages), got.Usage.TotalTokens, wantMessages)
			}
			if len(m.sessions) != 0 {
				t.Error("session lock was retained after both operations")
			}
		})
	}
}

func TestManager_Create(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage)

	opts := CreateOptions{
		TenantID: "tenant-1",
		UserID:   "user-1",
		AgentID:  "agent-1",
		Model:    "claude-3-opus",
	}

	session, err := manager.Create(t.Context(), opts)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if session.ID == "" {
		t.Error("Session ID should not be empty")
	}

	if session.TenantID != opts.TenantID {
		t.Errorf("TenantID = %v, want %v", session.TenantID, opts.TenantID)
	}

	if session.UserID != opts.UserID {
		t.Errorf("UserID = %v, want %v", session.UserID, opts.UserID)
	}
}

func TestManager_Get(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage)

	// Create session
	created, err := manager.Create(t.Context(), CreateOptions{
		TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Get session
	retrieved, err := manager.Get(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if retrieved.ID != created.ID {
		t.Errorf("ID = %v, want %v", retrieved.ID, created.ID)
	}
}

func TestManager_Get_NotFound(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage)

	_, err := manager.Get(t.Context(), "nonexistent")
	if err == nil {
		t.Error("Get() should return error for nonexistent session")
	}
}

func TestManager_Get_Expired(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage)

	// Create session with very short TTL
	session, err := manager.Create(t.Context(), CreateOptions{
		TenantID: "tenant-1",
		TTL:      1 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Try to get expired session
	_, err = manager.Get(t.Context(), session.ID)
	if err == nil {
		t.Error("Get() should return error for expired session")
	}
}

func TestManager_AppendMessage(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage, WithTokenizer(tokenizer.NewSimpleTokenizer("test")))

	session, err := manager.Create(t.Context(), CreateOptions{
		TenantID: "tenant-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Append message
	msg := provider.NewTextMessage(provider.RoleUser, "Hello, world!")
	if err := manager.AppendMessage(t.Context(), session.ID, msg); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}

	// Verify message was added
	updated, err := manager.Get(t.Context(), session.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if len(updated.Messages) != 1 {
		t.Errorf("Message count = %v, want 1", len(updated.Messages))
	}

	if updated.Messages[0].GetText() != "Hello, world!" {
		t.Errorf("Message text = %v, want 'Hello, world!'", updated.Messages[0].GetText())
	}
}

func TestManager_AppendMessage_MessageLimitExceeded(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage, WithConfig(Config{
		DefaultTTL:  24 * time.Hour,
		MaxMessages: 1,
		MaxTokens:   1000,
		AutoTrim:    false,
	}))

	session, err := manager.Create(t.Context(), CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := manager.AppendMessage(t.Context(), session.ID, provider.NewTextMessage(provider.RoleUser, "first")); err != nil {
		t.Fatalf("AppendMessage(first) error = %v", err)
	}

	err = manager.AppendMessage(t.Context(), session.ID, provider.NewTextMessage(provider.RoleUser, "second"))
	if err == nil {
		t.Fatal("AppendMessage(second) should fail when message limit is exceeded")
	}

	updated, err := manager.Get(t.Context(), session.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(updated.Messages) != 1 {
		t.Fatalf("stored message count = %d, want 1", len(updated.Messages))
	}
	if updated.Messages[0].GetText() != "first" {
		t.Fatalf("stored message = %q, want first", updated.Messages[0].GetText())
	}
}

func TestManager_GetMessages(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage)

	session, err := manager.Create(t.Context(), CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Add multiple messages
	messages := []string{"Message 1", "Message 2", "Message 3", "Message 4", "Message 5"}
	for _, text := range messages {
		msg := provider.NewTextMessage(provider.RoleUser, text)
		if err := manager.AppendMessage(t.Context(), session.ID, msg); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}

	// Get messages with pagination
	retrieved, err := manager.GetMessages(t.Context(), session.ID, 1, 3)
	if err != nil {
		t.Fatalf("GetMessages() error = %v", err)
	}

	if len(retrieved) != 3 {
		t.Errorf("Message count = %v, want 3", len(retrieved))
	}

	if retrieved[0].GetText() != "Message 2" {
		t.Errorf("First message = %v, want 'Message 2'", retrieved[0].GetText())
	}
}

func TestManager_GetActiveContext(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage, WithTokenizer(tokenizer.NewSimpleTokenizer("test")))

	session, err := manager.Create(t.Context(), CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Add messages
	for i := 0; i < 10; i++ {
		msg := provider.NewTextMessage(provider.RoleUser, "This is a test message")
		if err := manager.AppendMessage(t.Context(), session.ID, msg); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}

	// Get active context with token limit
	active, err := manager.GetActiveContext(t.Context(), session.ID, 50)
	if err != nil {
		t.Fatalf("GetActiveContext() error = %v", err)
	}

	// Should return fewer messages to fit token budget
	if len(active) >= 10 {
		t.Errorf("Active context should be trimmed, got %v messages", len(active))
	}
}

func TestManager_SetContext(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage)

	session, err := manager.Create(t.Context(), CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Set context
	if err := manager.SetContext(t.Context(), session.ID, "key1", "value1"); err != nil {
		t.Fatalf("SetContext() error = %v", err)
	}

	// Get context
	value, err := manager.GetContext(t.Context(), session.ID, "key1")
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}

	if value != "value1" {
		t.Errorf("Context value = %v, want 'value1'", value)
	}
}

func TestManager_GetContext_NotFound(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage)

	session, err := manager.Create(t.Context(), CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if _, err := manager.GetContext(t.Context(), session.ID, "missing"); err == nil {
		t.Fatal("GetContext() should return error for missing key")
	}
}

func TestManager_Delete(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage)

	session, err := manager.Create(t.Context(), CreateOptions{})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Delete session
	if err := manager.Delete(t.Context(), session.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify deletion
	_, err = manager.Get(t.Context(), session.ID)
	if err == nil {
		t.Error("Get() should return error for deleted session")
	}
}

func TestMemoryStorage(t *testing.T) {
	storage := NewMemoryStorage()

	session := &Session{
		ID:       "test-1",
		TenantID: "tenant-1",
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, "Hello"),
		},
		Context: map[string]any{
			"key": "value",
		},
	}

	// Save
	if err := storage.Save(t.Context(), session); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Load
	loaded, err := storage.Load(t.Context(), "test-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if loaded.ID != session.ID {
		t.Errorf("ID = %v, want %v", loaded.ID, session.ID)
	}

	// Delete
	if err := storage.Delete(t.Context(), "test-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	// Verify deletion
	_, err = storage.Load(t.Context(), "test-1")
	if err == nil {
		t.Error("Load() should return error for deleted session")
	}
}

func TestMemoryStorage_Count(t *testing.T) {
	storage := NewMemoryStorage()

	if count := storage.Count(); count != 0 {
		t.Errorf("Initial count = %v, want 0", count)
	}

	// Add sessions
	for i := 0; i < 5; i++ {
		session := &Session{ID: fmt.Sprintf("sess-%d", i)}
		_ = storage.Save(t.Context(), session)
	}

	if count := storage.Count(); count != 5 {
		t.Errorf("Count = %v, want 5", count)
	}

	// Clear
	storage.Clear()

	if count := storage.Count(); count != 0 {
		t.Errorf("Count after Clear() = %v, want 0", count)
	}
}

func TestManager_Update(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage)

	sess, err := manager.Create(t.Context(), CreateOptions{TenantID: "t1"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	sess.AgentID = "updated-agent"
	if err := manager.Update(t.Context(), sess); err != nil {
		t.Fatalf("Update error: %v", err)
	}

	loaded, err := manager.Get(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if loaded.AgentID != "updated-agent" {
		t.Errorf("AgentID = %q, want updated-agent", loaded.AgentID)
	}
	if loaded.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be set after Update")
	}
}

func TestManager_TrimMessages_AutoTrim(t *testing.T) {
	storage := NewMemoryStorage()
	manager := NewManager(storage, WithConfig(Config{
		DefaultTTL:  24 * time.Hour,
		MaxMessages: 100,
		MaxTokens:   10, // very small to force trim
		AutoTrim:    true,
	}), WithTokenizer(noopTokenizer{}))

	sess, err := manager.Create(t.Context(), CreateOptions{})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Add messages — token counter increments, trim kicks in on overflow.
	for i := 0; i < 5; i++ {
		msg := provider.NewTextMessage(provider.RoleUser, "msg")
		if err := manager.AppendMessage(t.Context(), sess.ID, msg); err != nil {
			t.Fatalf("AppendMessage #%d error: %v", i, err)
		}
	}

	loaded, err := manager.Get(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	// After auto-trim, messages should be fewer than what was added.
	if len(loaded.Messages) >= 5 {
		t.Errorf("expected trim to reduce messages below 5, got %d", len(loaded.Messages))
	}
}

// noopTokenizer returns 3 tokens per text to make MaxTokens easy to exceed.
type noopTokenizer struct{}

func (noopTokenizer) Count(text string) (int, error)                      { return 3, nil }
func (noopTokenizer) CountMessages(msgs []tokenizer.Message) (int, error) { return 3 * len(msgs), nil }
func (noopTokenizer) ModelName() string                                   { return "noop" }

func TestManagerGetMessagesPaginationBounds(t *testing.T) {
	manager := NewManager(NewMemoryStorage())
	session, err := manager.Create(t.Context(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first", "second"} {
		if err := manager.AppendMessage(t.Context(), session.ID, provider.NewTextMessage(provider.RoleUser, text)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		offset, limit, count int
		wantError            bool
	}{
		{-1, 1, 0, true},
		{0, -1, 0, true},
		{1, math.MaxInt, 1, false},
		{2, 3, 0, false},
		{0, 0, 0, false},
	} {
		messages, err := manager.GetMessages(t.Context(), session.ID, tc.offset, tc.limit)
		if (err != nil) != tc.wantError || len(messages) != tc.count {
			t.Errorf("GetMessages(%d, %d) returned %d messages, err = %v", tc.offset, tc.limit, len(messages), err)
		}
	}
}

type expirationTestStorage struct {
	*MemoryStorage
	deleteErr error
	calls     int
}

func (s *expirationTestStorage) Delete(ctx context.Context, id string) error {
	s.calls++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.MemoryStorage.Delete(ctx, id)
}

func TestExpiredSessionReportsCleanupFailure(t *testing.T) {
	ctx := t.Context()
	const id = "expired"
	sentinel := errors.New("test cleanup error")
	storage := &expirationTestStorage{MemoryStorage: NewMemoryStorage(), deleteErr: sentinel}
	if err := storage.Save(ctx, &Session{ID: id, ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(storage)
	if got, err := manager.Get(ctx, id); got != nil || !errors.Is(err, sentinel) || storage.Count() != 1 {
		t.Fatalf("Get = %v, %v, stored = %d", got, err, storage.Count())
	}
	storage.deleteErr = nil
	if got, err := manager.Get(ctx, id); got != nil || err == nil || err.Error() != "session expired" || storage.Count() != 0 || storage.calls != 2 {
		t.Fatalf("retry Get = %v, %v, stored = %d, delete calls = %d", got, err, storage.Count(), storage.calls)
	}
}

func TestTrimMessagesPersistsNewestMessages(t *testing.T) {
	ctx := t.Context()
	storage := NewMemoryStorage()
	manager := NewManager(storage, WithTokenizer(noopTokenizer{}))
	session, err := manager.Create(ctx, CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"oldest", "newer", "newest"} {
		if err := manager.AppendMessage(ctx, session.ID, provider.NewTextMessage(provider.RoleUser, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.TrimMessages(ctx, session.ID, 6); err != nil {
		t.Fatal(err)
	}
	stored, err := storage.Load(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Messages) != 2 || stored.Messages[0].GetText() != "newer" || stored.Messages[1].GetText() != "newest" || stored.Usage.TotalTokens != 6 {
		t.Errorf("persisted session = %+v", stored)
	}
	_, wantErr := storage.Load(ctx, "missing")
	if err := manager.TrimMessages(ctx, "missing", 6); wantErr == nil || err == nil || err.Error() != wantErr.Error() {
		t.Errorf("missing session: TrimMessages=%v Load=%v", err, wantErr)
	}
}
