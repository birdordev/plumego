package session

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"

	"github.com/birdordev/plumego/x/ai/provider"
)

// MemoryStorage stores snapshots of session fields, structured messages, and
// JSON context containers (map[string]any and []any). Other context values
// with mutable references must be treated as immutable by callers.
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

	s.sessions[session.ID] = cloneSession(session)
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

	return cloneSession(session), nil
}

func cloneSession(session *Session) *Session {
	copy := *session
	cloner := make(sessionValueCloner)
	copy.Metadata = maps.Clone(session.Metadata)
	copy.Context = cloner.cloneMap(session.Context)
	if copy.Context == nil {
		copy.Context = make(map[string]any)
	}
	copy.Messages = append(make([]provider.Message, 0, len(session.Messages)), session.Messages...)
	for i := range copy.Messages {
		msg := &copy.Messages[i]
		if blocks, ok := msg.Content.([]provider.ContentBlock); ok {
			msg.Content = cloner.cloneBlocks(blocks)
		}
	}
	return &copy
}

func (c sessionValueCloner) cloneBlocks(blocks []provider.ContentBlock) []provider.ContentBlock {
	copy := slices.Clone(blocks)
	for i := range copy {
		block := &copy[i]
		if block.ToolUse != nil {
			use := *block.ToolUse
			use.Input = c.cloneMap(use.Input)
			block.ToolUse = &use
		}
		if block.ToolResult != nil {
			result := *block.ToolResult
			block.ToolResult = &result
		}
		if block.Image != nil {
			image := *block.Image
			block.Image = &image
		}
	}
	return copy
}

// Register each cloned JSON container before descending so shared references
// and cycles are preserved within the snapshot.
type sessionValueKey struct {
	ptr     uintptr
	length  int
	isSlice bool
}

type sessionValueCloner map[sessionValueKey]any

func (c sessionValueCloner) cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	key := sessionValueKey{ptr: reflect.ValueOf(value).Pointer()}
	if copy, ok := c[key]; ok {
		return copy.(map[string]any)
	}
	copy := make(map[string]any, len(value))
	c[key] = copy
	for name, entry := range value {
		copy[name] = c.cloneValue(entry)
	}
	return copy
}

func (c sessionValueCloner) cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return c.cloneMap(value)
	case []any:
		if len(value) == 0 {
			return slices.Clone(value)
		}
		key := sessionValueKey{ptr: reflect.ValueOf(value).Pointer(), length: len(value), isSlice: true}
		if copy, ok := c[key]; ok {
			return copy
		}
		copy := make([]any, len(value))
		c[key] = copy
		for i, entry := range value {
			copy[i] = c.cloneValue(entry)
		}
		return copy
	default:
		return value
	}
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
