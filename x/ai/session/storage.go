package session

import (
	"context"
	"fmt"
	"sync"

	"github.com/birdordev/plumego/x/ai/provider"
)

// MemoryStorage implements in-memory session storage.
type MemoryStorage struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewMemoryStorage creates a new memory storage.
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		sessions: make(map[string]*Session),
	}
}

// Save implements Storage.
func (s *MemoryStorage) Save(ctx context.Context, session *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Deep copy to avoid mutation
	copied := *session
	copied.Messages = make([]provider.Message, len(session.Messages))
	copy(copied.Messages, session.Messages)

	copied.Context = make(map[string]any)
	for k, v := range session.Context {
		copied.Context[k] = v
	}

	s.sessions[session.ID] = &copied
	return nil
}

// Load implements Storage.
func (s *MemoryStorage) Load(ctx context.Context, sessionID string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	// Deep copy to avoid external mutation
	copied := *session
	copied.Messages = make([]provider.Message, len(session.Messages))
	copy(copied.Messages, session.Messages)

	copied.Context = make(map[string]any)
	for k, v := range session.Context {
		copied.Context[k] = v
	}

	return &copied, nil
}

// Delete implements Storage.
func (s *MemoryStorage) Delete(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, sessionID)
	return nil
}

// Count returns the number of sessions.
func (s *MemoryStorage) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// Clear removes all sessions.
func (s *MemoryStorage) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = make(map[string]*Session)
}
