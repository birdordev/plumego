package websocket

import (
	"context"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

type hubJob struct {
	conn *Conn
	op   byte
	data []byte
}

const hubWorkerDefaultSendTimeout = 100 * time.Millisecond

// BroadcastResult describes a broadcast fanout attempt.
type BroadcastResult struct {
	Attempted int  `json:"attempted"`
	Enqueued  int  `json:"enqueued"`
	Skipped   int  `json:"skipped"`
	Dropped   int  `json:"dropped"`
	Invalid   bool `json:"invalid"`
	Stopped   bool `json:"stopped"`
}

// Rejected reports whether the broadcast targeted at least one connection but
// could not enqueue the message for any connection, or was rejected before
// fanout because the input was invalid or the hub was stopped.
func (r BroadcastResult) Rejected() bool {
	return r.Invalid || r.Stopped || (r.Attempted > 0 && r.Enqueued == 0)
}

// Hub manages rooms and broadcast.
//
// Hub provides an experimental WebSocket hub with:
//   - Room-based message broadcasting
//   - Worker pool for concurrent message delivery
//   - Connection limits (total and per-room)
//   - Metrics collection
//   - Security event monitoring
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	// Create hub with 4 workers and 1024 job queue size
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	defer hub.Stop()
//
//	// Join a connection to a room
//	err := hub.TryJoin("chat-room", conn)
//	if err != nil {
//		// Handle capacity limits
//	}
//
//	// Broadcast to a room
//	hub.BroadcastRoom("chat-room", websocket.OpcodeText, []byte("Hello"))
//
//	// Get metrics
//	metrics := hub.Metrics()
type Hub struct {
	rooms map[string]map[*Conn]struct{}
	mu    sync.RWMutex

	// worker pool
	jobQueue    chan hubJob
	workers     int
	wg          sync.WaitGroup
	quit        chan struct{}
	lifecycleMu sync.RWMutex

	// stopped is set to true by Stop() before closing quit.
	// BroadcastRoom/BroadcastAll check this to avoid sending on a channel
	// whose readers have already exited, preventing any post-stop confusion.
	stopped atomic.Bool

	maxRoomRegistrations int
	maxRoomConns         int
	roomRegistrations    atomic.Uint64 // Atomic counter for active room registrations
	accepted             atomic.Uint64
	rejected             atomic.Uint64

	// Per-hub security/broadcast metrics (replaces global securityMetrics writes)
	broadcastAttempted atomic.Uint64 // broadcast target attempts
	broadcastEnqueued  atomic.Uint64 // messages enqueued to workers
	broadcastSkipped   atomic.Uint64 // targets skipped before enqueue
	broadcastDropped   atomic.Uint64 // messages dropped due to full job queue
	securityRejections atomic.Uint64 // connections rejected (origin/auth/capacity)
	invalidWSKeys      atomic.Uint64 // invalid Sec-WebSocket-Key headers seen
	successfulAuths    atomic.Uint64 // successful JWT authentications

	// Rate limiting (simple token bucket implementation)
	rateLimiter *simpleRateLimiter

	// Message pooling to reduce allocations
	connListPool sync.Pool // Pool of []*Conn slices

	// Hub configuration
	config HubConfig
	// Logger for production events
	logger *log.Logger
	// Channel for security events
	securityEvents chan securityEvent
}

// getConnList gets a connection list from the pool
func (h *Hub) getConnList() *[]*Conn {
	return h.connListPool.Get().(*[]*Conn)
}

// putConnList returns a connection list to the pool after clearing it
func (h *Hub) putConnList(conns *[]*Conn) {
	// Clear the slice but keep the underlying array
	*conns = (*conns)[:0]
	h.connListPool.Put(conns)
}

func (h *Hub) roomNameValidator() RoomNameValidator {
	if h == nil {
		return defaultRoomNameValidator
	}
	return h.config.RoomNameValidator
}

// simpleRateLimiter implements a basic token bucket rate limiter using only standard library.
// The mutex protects all fields; plain integer fields are used since the mutex already
// provides mutual exclusion (atomic types inside a mutex are redundant).
type simpleRateLimiter struct {
	rate       int // Tokens per second
	burst      int // Maximum burst size
	tokens     uint64
	lastRefill int64 // nanoseconds
	mu         sync.Mutex
}

// newRateLimiter creates a rate limiter that allows 'rate' events per second with burst capacity
func newRateLimiter(rate, burst int) *simpleRateLimiter {
	if rate <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = rate
	}
	return &simpleRateLimiter{
		rate:       rate,
		burst:      burst,
		tokens:     uint64(burst * 1000), // Start with full burst capacity
		lastRefill: time.Now().UnixNano(),
	}
}

// allow checks if an event can proceed under rate limits
func (rl *simpleRateLimiter) allow() bool {
	if rl == nil {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now().UnixNano()
	elapsed := now - rl.lastRefill

	// Integer-only token refill: milliTokens per nanosecond = rate/1_000_000.
	// Avoids float64 arithmetic on the hot connection-accept path.
	tokensToAdd := elapsed * int64(rl.rate) / 1_000_000
	if tokensToAdd > 0 {
		maxTokens := uint64(rl.burst * 1000)
		rl.tokens += uint64(tokensToAdd)
		if rl.tokens > maxTokens {
			rl.tokens = maxTokens
		}
		rl.lastRefill = now
	}

	// Try to consume one token
	if rl.tokens >= 1000 {
		rl.tokens -= 1000
		return true
	}

	return false
}

type securityEvent struct {
	Timestamp time.Time
	Type      string
	Details   map[string]any
	Severity  string // "info", "warning", "error"
}

// HubConfig configures a hub instance with production features.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	config := websocket.HubConfig{
//		WorkerCount:            4,
//		JobQueueSize:           1024,
//		MaxRoomRegistrations: 10000,
//		MaxRoomConnections:     100,
//		EnableDebugLogging:     true,
//		RejectOnQueueFull:      true,
//		MaxConnectionRate:      100, // 100 connections per second
//		EnableSecurityMetrics:  true,
//	}
//	hub, err := websocket.NewHubWithConfig(config)
type HubConfig struct {
	// WorkerCount is the number of worker goroutines for message delivery
	WorkerCount int

	// JobQueueSize is the size of the job queue
	JobQueueSize int

	// MaxRoomRegistrations is the maximum active room registrations allowed.
	// A single connection joined to N rooms contributes N registrations.
	MaxRoomRegistrations int

	// MaxRoomConnections is the maximum connections per room
	MaxRoomConnections int

	// EnableDebugLogging enables detailed logging for debugging
	EnableDebugLogging bool

	// RejectOnQueueFull determines behavior when broadcast queue is full
	// true: reject message and log error
	// false: drop message silently
	RejectOnQueueFull bool

	// MaxConnectionRate limits new connections per second
	// 0 means no limit
	MaxConnectionRate int

	// RoomNameValidator validates application room identifiers for public hub
	// room APIs. Nil uses the default validator.
	RoomNameValidator RoomNameValidator

	// EnableSecurityMetrics enables the internal security event monitor.
	// Metrics counters are always collected in HubMetrics.
	EnableSecurityMetrics bool

	// Logger is optional and caller-owned. When nil, the hub discards logs.
	Logger *log.Logger
}

// HubMetrics describes hub connection metrics.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	metrics := hub.Metrics()
//	fmt.Printf("Active: %d, Rooms: %d\n", metrics.ActiveConnections, metrics.Rooms)
type HubMetrics struct {
	// ActiveConnections is the number of unique open connections registered in
	// at least one room.
	ActiveConnections int `json:"active_connections"`
	// RoomRegistrations is the sum of (connection x room) registrations.
	// A connection joined to N rooms contributes N to this count.
	RoomRegistrations    int    `json:"room_registrations"`
	Rooms                int    `json:"rooms"`
	AcceptedTotal        uint64 `json:"accepted_total"`
	RejectedTotal        uint64 `json:"rejected_total"`
	MaxRoomRegistrations int    `json:"max_room_registrations"`
	MaxRoomConnections   int    `json:"max_room_connections"`
	// Per-hub security/broadcast metrics
	BroadcastAttempted uint64 `json:"broadcast_attempted"`
	BroadcastEnqueued  uint64 `json:"broadcast_enqueued"`
	BroadcastSkipped   uint64 `json:"broadcast_skipped"`
	BroadcastDropped   uint64 `json:"broadcast_dropped"`
	SecurityRejections uint64 `json:"security_rejections"`
	InvalidWSKeys      uint64 `json:"invalid_ws_keys"`
	SuccessfulAuths    uint64 `json:"successful_auths"`
}

// NewHubWithConfig creates a new WebSocket hub with custom configuration and
// returns explicit validation errors for invalid public inputs.
func NewHubWithConfig(cfg HubConfig) (*Hub, error) {
	if cfg.WorkerCount < 0 {
		return nil, ErrNegativeWorkerCount
	}
	if cfg.JobQueueSize < 0 {
		return nil, ErrNegativeJobQueue
	}
	if cfg.MaxRoomRegistrations < 0 || cfg.MaxRoomConnections < 0 || cfg.MaxConnectionRate < 0 {
		return nil, ErrNegativeLimit
	}
	if cfg.WorkerCount == 0 {
		cfg.WorkerCount = 4
	}
	if cfg.JobQueueSize == 0 {
		cfg.JobQueueSize = 1024
	}
	return newHubWithNormalizedConfig(cfg)
}

// NewHub creates a hub from worker and queue sizes.
//
// It delegates to NewHubWithConfig.
func NewHub(workerCount, jobQueueSize int) (*Hub, error) {
	return NewHubWithConfig(HubConfig{
		WorkerCount:  workerCount,
		JobQueueSize: jobQueueSize,
	})
}

func newHubWithNormalizedConfig(cfg HubConfig) (*Hub, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	h := &Hub{
		rooms:                make(map[string]map[*Conn]struct{}),
		jobQueue:             make(chan hubJob, cfg.JobQueueSize),
		workers:              cfg.WorkerCount,
		quit:                 make(chan struct{}),
		maxRoomRegistrations: cfg.MaxRoomRegistrations,
		maxRoomConns:         cfg.MaxRoomConnections,
		config:               cfg,
		securityEvents:       make(chan securityEvent, 100),
		logger:               logger,
	}

	// Initialize connection list pool
	h.connListPool = sync.Pool{
		New: func() any {
			// Pre-allocate with reasonable capacity
			conns := make([]*Conn, 0, 64)
			return &conns
		},
	}

	// Initialize rate limiter if configured
	if cfg.MaxConnectionRate > 0 {
		// Allow burst up to 2x the rate for handling spikes
		burst := cfg.MaxConnectionRate * 2
		h.rateLimiter = newRateLimiter(cfg.MaxConnectionRate, burst)
		if cfg.EnableDebugLogging {
			h.logger.Printf("Rate limiter initialized: %d connections/sec (burst: %d)", cfg.MaxConnectionRate, burst)
		}
	}

	h.startWorkers()
	h.startSecurityMonitor()
	return h, nil
}

func (h *Hub) startWorkers() {
	for i := 0; i < h.workers; i++ {
		h.wg.Add(1)
		go func(workerID int) {
			defer h.wg.Done()
			for {
				select {
				case j, ok := <-h.jobQueue:
					if !ok {
						return
					}
					// Write with error handling
					err := h.writeJob(j)
					if err != nil {
						if h.config.EnableDebugLogging {
							h.logger.Printf("Worker %d: failed to write to connection: %v", workerID, err)
						}
						if h.config.EnableSecurityMetrics {
							h.recordSecurityEvent("broadcast_error", map[string]any{
								"error":  err.Error(),
								"worker": workerID,
							}, "warning")
						}
					}
				case <-h.quit:
					// Drain remaining jobs so in-flight messages are not silently dropped.
					// No new sends arrive after Stop() sets stopped=true before closing quit,
					// so this loop terminates as soon as the buffered channel is empty.
					for {
						select {
						case j := <-h.jobQueue:
							_ = h.writeJob(j)
						default:
							return
						}
					}
				}
			}
		}(i)
	}
}

func (h *Hub) writeJob(j hubJob) error {
	if j.conn.sendBehavior != SendBlock || j.conn.sendTimeout > 0 {
		return j.conn.WriteMessage(j.op, j.data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), hubWorkerDefaultSendTimeout)
	defer cancel()
	return j.conn.WriteMessageContext(ctx, j.op, j.data)
}

// startSecurityMonitor processes security events
func (h *Hub) startSecurityMonitor() {
	if !h.config.EnableSecurityMetrics {
		return
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			select {
			case event := <-h.securityEvents:
				if h.config.EnableDebugLogging {
					h.logger.Printf("[%s] %s: %v", event.Severity, event.Type, event.Details)
				}
			case <-h.quit:
				// Drain remaining security events before exiting.
				for {
					select {
					case event := <-h.securityEvents:
						if h.config.EnableDebugLogging {
							h.logger.Printf("[%s] %s: %v", event.Severity, event.Type, event.Details)
						}
					default:
						return
					}
				}
			}
		}
	}()
}

// recordSecurityEvent records a security event
func (h *Hub) recordSecurityEvent(eventType string, details map[string]any, severity string) {
	select {
	case h.securityEvents <- securityEvent{
		Timestamp: time.Now(),
		Type:      eventType,
		Details:   details,
		Severity:  severity,
	}:
	default:
		// Channel full, drop event
	}
}

// Stop gracefully shuts down the hub.
//
// Stop is idempotent — calling it multiple times is safe.
//
// This method:
//   - Marks the hub as stopped so new broadcasts are rejected immediately
//   - Signals all worker goroutines to exit via the quit channel
//   - Waits for all workers and the security monitor to finish
//   - Clears room registrations and room membership metrics
//
// Note: h.jobQueue is intentionally NOT closed here. Workers exit via the quit
// channel, so closing the queue is unnecessary. More importantly, closing it
// while a concurrent BroadcastRoom/BroadcastAll is still executing a channel
// send would cause a panic ("send on closed channel"). The stopped flag prevents
// new sends after Stop is called.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	defer hub.Stop()
func (h *Hub) Stop() {
	if !h.stopped.CompareAndSwap(false, true) {
		return // already stopped
	}
	h.lifecycleMu.Lock()
	close(h.quit)
	h.lifecycleMu.Unlock()
	h.wg.Wait()
	h.clearRooms()
}

// Shutdown closes all open connections and then stops the hub.
//
// It collects every unique connection across all rooms, calls Close() on each
// one to tear down TCP immediately, and finally calls Stop() to drain in-flight
// jobs and shut down workers. Shutdown intentionally does not send WebSocket
// close frames because clean close-frame delivery can block on slow clients
// during process shutdown.
//
// ctx controls the overall deadline for the close loop. A nil context is treated
// as context.Background(). If the context is
// cancelled before all connections are closed, Shutdown returns ctx.Err()
// immediately after stopping the hub and clearing room registrations.
//
// Example:
//
//	import (
//	    "context"
//	    "time"
//	    "github.com/birdordev/plumego/x/websocket"
//	)
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	if err := hub.Shutdown(ctx); err != nil {
//	    log.Printf("shutdown incomplete: %v", err)
//	}
func (h *Hub) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// Collect all unique connections under the read lock.
	h.mu.RLock()
	seen := make(map[*Conn]struct{})
	for _, rs := range h.rooms {
		for c := range rs {
			if c == nil {
				continue
			}
			seen[c] = struct{}{}
		}
	}
	h.mu.RUnlock()

	// Close each connection, respecting context cancellation.
	for c := range seen {
		select {
		case <-ctx.Done():
			h.Stop()
			return ctx.Err()
		default:
		}
		_ = c.Close()
	}

	h.clearRooms()
	h.Stop()
	return nil
}
