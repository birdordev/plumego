// Package session provides conversation session management for AI agents.
//
// Stability: stable — this package follows semantic versioning guarantees.
// Breaking changes require a major version bump.
package session

import (
	"context"
	"fmt"
	"time"

	"github.com/spcent/plumego/x/ai/provider"
	"github.com/spcent/plumego/x/ai/tokenizer"
)

// Session represents an AI conversation session.
type Session struct {
	ID        string
	TenantID  string
	UserID    string
	AgentID   string
	Model     string
	Messages  []provider.Message
	Context   map[string]any
	Usage     tokenizer.TokenUsage
	CreatedAt time.Time
	UpdatedAt time.Time
	ExpiresAt time.Time
	Metadata  map[string]string
}

// Manager manages conversation sessions.
type Manager struct {
	storage   Storage
	tokenizer tokenizer.Tokenizer
	config    Config
}

// Config configures the session manager.
type Config struct {
	// Default session TTL
	DefaultTTL time.Duration

	// Maximum messages per session
	MaxMessages int

	// Maximum tokens per session (context window)
	MaxTokens int

	// Auto-trim when exceeding token limit
	AutoTrim bool
}

// DefaultConfig returns default configuration.
func DefaultConfig() Config {
	return Config{
		DefaultTTL:  24 * time.Hour,
		MaxMessages: 1000,
		MaxTokens:   100000,
		AutoTrim:    true,
	}
}

// NewManager creates a new session manager.
func NewManager(storage Storage, opts ...Option) *Manager {
	m := &Manager{
		storage:   storage,
		tokenizer: tokenizer.NewSimpleTokenizer("default"),
		config:    DefaultConfig(),
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Option configures the manager.
type Option func(*Manager)

// WithTokenizer sets the tokenizer.
func WithTokenizer(t tokenizer.Tokenizer) Option {
	return func(m *Manager) {
		m.tokenizer = t
	}
}

// WithConfig sets the configuration.
func WithConfig(config Config) Option {
	return func(m *Manager) {
		m.config = config
	}
}

// CreateOptions configure session creation.
type CreateOptions struct {
	TenantID string
	UserID   string
	AgentID  string
	Model    string
	TTL      time.Duration
	Metadata map[string]string
}

// Create creates a new session.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (*Session, error) {
	now := time.Now()

	ttl := opts.TTL
	if ttl == 0 {
		ttl = m.config.DefaultTTL
	}

	session := &Session{
		ID:        generateID(),
		TenantID:  opts.TenantID,
		UserID:    opts.UserID,
		AgentID:   opts.AgentID,
		Model:     opts.Model,
		Messages:  make([]provider.Message, 0),
		Context:   make(map[string]any),
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(ttl),
		Metadata:  opts.Metadata,
	}

	if err := m.storage.Save(ctx, session); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	return session, nil
}

// Get retrieves a session.
func (m *Manager) Get(ctx context.Context, sessionID string) (*Session, error) {
	session, err := m.storage.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	// Check expiration
	if time.Now().After(session.ExpiresAt) {
		_ = m.storage.Delete(ctx, sessionID)
		return nil, fmt.Errorf("session expired")
	}

	return session, nil
}

// Update updates a session.
func (m *Manager) Update(ctx context.Context, session *Session) error {
	session.UpdatedAt = time.Now()
	return m.storage.Save(ctx, session)
}

// Delete deletes a session.
func (m *Manager) Delete(ctx context.Context, sessionID string) error {
	return m.storage.Delete(ctx, sessionID)
}

// AppendMessage appends a message to the session.
func (m *Manager) AppendMessage(ctx context.Context, sessionID string, msg provider.Message) error {
	session, err := m.Get(ctx, sessionID)
	if err != nil {
		return err
	}

	// Append message
	session.Messages = append(session.Messages, msg)

	// Update token usage
	if msgTokens, err := m.countMessageTokens(msg); err == nil {
		session.Usage.TotalTokens += msgTokens
	}

	// Check limits
	if len(session.Messages) > m.config.MaxMessages {
		return fmt.Errorf("message limit exceeded")
	}

	// Auto-trim if needed
	if m.config.AutoTrim && session.Usage.TotalTokens > m.config.MaxTokens {
		if err := m.trimMessages(session); err != nil {
			return fmt.Errorf("trim messages: %w", err)
		}
	}

	return m.Update(ctx, session)
}

// GetMessages retrieves messages with pagination.
func (m *Manager) GetMessages(ctx context.Context, sessionID string, offset, limit int) ([]provider.Message, error) {
	session, err := m.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	messages := session.Messages

	// Apply pagination
	if offset >= len(messages) {
		return []provider.Message{}, nil
	}

	end := offset + limit
	if end > len(messages) {
		end = len(messages)
	}

	return messages[offset:end], nil
}

// GetActiveContext returns messages that fit within the token budget.
func (m *Manager) GetActiveContext(ctx context.Context, sessionID string, maxTokens int) ([]provider.Message, error) {
	session, err := m.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	return m.selectMessages(session.Messages, maxTokens)
}

// TrimMessages removes old messages to fit within token budget.
func (m *Manager) TrimMessages(ctx context.Context, sessionID string, maxTokens int) error {
	session, err := m.Get(ctx, sessionID)
	if err != nil {
		return err
	}

	if err := m.trimMessagesToLimit(session, maxTokens); err != nil {
		return err
	}

	return m.Update(ctx, session)
}

// SetContext sets a context variable.
func (m *Manager) SetContext(ctx context.Context, sessionID string, key string, value any) error {
	session, err := m.Get(ctx, sessionID)
	if err != nil {
		return err
	}

	session.Context[key] = value
	return m.Update(ctx, session)
}

// GetContext gets a context variable.
func (m *Manager) GetContext(ctx context.Context, sessionID string, key string) (any, error) {
	session, err := m.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	value, ok := session.Context[key]
	if !ok {
		return nil, fmt.Errorf("context key not found: %s", key)
	}

	return value, nil
}

// selectMessages selects messages that fit within token budget.
func (m *Manager) selectMessages(messages []provider.Message, maxTokens int) ([]provider.Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	// Count tokens from newest to oldest
	totalTokens := 0
	selected := make([]provider.Message, 0)

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		msgTokens, err := m.countMessageTokens(msg)
		if err != nil {
			continue
		}

		if totalTokens+msgTokens > maxTokens {
			break
		}

		selected = append([]provider.Message{msg}, selected...)
		totalTokens += msgTokens
	}

	return selected, nil
}

// trimMessages trims old messages automatically.
func (m *Manager) trimMessages(session *Session) error {
	return m.trimMessagesToLimit(session, m.config.MaxTokens)
}

// trimMessagesToLimit trims messages to fit within token limit.
func (m *Manager) trimMessagesToLimit(session *Session, maxTokens int) error {
	selected, err := m.selectMessages(session.Messages, maxTokens)
	if err != nil {
		return err
	}

	session.Messages = selected

	// Recalculate usage
	session.Usage.TotalTokens = 0
	for _, msg := range selected {
		if msgTokens, err := m.countMessageTokens(msg); err == nil {
			session.Usage.TotalTokens += msgTokens
		}
	}

	return nil
}

// countMessageTokens counts tokens in a message.
func (m *Manager) countMessageTokens(msg provider.Message) (int, error) {
	text := msg.GetText()
	return m.tokenizer.Count(text)
}

// Storage interface for session persistence.
type Storage interface {
	Save(ctx context.Context, session *Session) error
	Load(ctx context.Context, sessionID string) (*Session, error)
	Delete(ctx context.Context, sessionID string) error
}

// generateID generates a unique session ID.
func generateID() string {
	// Simple ID generation (in production, use UUID or similar)
	return fmt.Sprintf("sess_%d", time.Now().UnixNano())
}
