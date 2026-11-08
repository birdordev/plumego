package websocket

func (h *Hub) clearRooms() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rooms = make(map[string]map[*Conn]struct{})
	h.roomRegistrations.Store(0)
}

// RangeConns calls fn for each non-closed connection in room.
//
// The iteration stops when fn returns false or all connections are visited.
// fn is called outside the hub lock, so it is safe to perform I/O or call
// other hub methods inside it.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub.RangeConns("chat-room", func(c *websocket.Conn) bool {
//	    if userID, ok := c.GetMetadata("user_id"); ok {
//	        fmt.Println("connected:", userID)
//	    }
//	    return true // continue
//	})
func (h *Hub) RangeConns(room string, fn func(*Conn) bool) {
	if err := validateRoomName(room, h.roomNameValidator()); err != nil {
		return
	}
	// Take a snapshot under the read lock so fn is called without holding it.
	conns := h.getConnList()
	defer h.putConnList(conns)

	h.mu.RLock()
	for c := range h.rooms[room] {
		if c != nil && !c.IsClosed() {
			*conns = append(*conns, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range *conns {
		if c == nil {
			continue
		}
		if !fn(c) {
			break
		}
	}
}

// TryJoin registers a connection in a room when limits allow it.
//
// Returns ErrHubFull if the hub has reached its maximum connection limit.
// Returns ErrRoomFull if the room has reached its maximum connection limit.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	conn, err := websocket.NewConn(...)
//	err := hub.TryJoin("chat-room", conn)
//	if err != nil {
//		if errors.Is(err, websocket.ErrHubFull) {
//			// Hub is at capacity
//		} else if errors.Is(err, websocket.ErrRoomFull) {
//			// Room is at capacity
//		}
//	}
func (h *Hub) TryJoin(room string, c *Conn) error {
	return h.tryJoin(room, c, h.roomNameValidator())
}

func (h *Hub) tryJoin(room string, c *Conn, validator RoomNameValidator) error {
	if h.stopped.Load() {
		h.rejected.Add(1)
		return ErrHubStopped
	}
	if err := validateRoomName(room, validator); err != nil {
		h.rejected.Add(1)
		return err
	}
	if c == nil {
		h.rejected.Add(1)
		return ErrNilConn
	}

	h.mu.RLock()
	if rs, ok := h.rooms[room]; ok {
		if _, exists := rs[c]; exists {
			h.mu.RUnlock()
			if h.config.EnableDebugLogging {
				h.logger.Printf("Connection already in room: %q", room)
			}
			return nil
		}
	}
	h.mu.RUnlock()

	// Rate limiting check (before acquiring lock for better performance)
	if h.rateLimiter != nil && !h.rateLimiter.allow() {
		h.rejected.Add(1)
		if h.config.EnableSecurityMetrics {
			h.recordSecurityEvent("rate_limit_exceeded", map[string]any{
				"room": room,
			}, "warning")
		}
		return ErrRateLimitExceeded
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if rs, ok := h.rooms[room]; ok {
		if _, exists := rs[c]; exists {
			if h.config.EnableDebugLogging {
				h.logger.Printf("Connection already in room: %q", room)
			}
			return nil
		}
	}

	// Fast path: check atomic counter for room registrations (O(1) instead of O(n))
	if h.maxRoomRegistrations > 0 {
		currentRegistrations := int(h.roomRegistrations.Load())
		if currentRegistrations >= h.maxRoomRegistrations {
			h.rejected.Add(1)
			if h.config.EnableSecurityMetrics {
				h.recordSecurityEvent("hub_full", map[string]any{
					"room":  room,
					"total": currentRegistrations,
				}, "warning")
			}
			return ErrHubFull
		}
	}

	rs, ok := h.rooms[room]
	if !ok {
		rs = make(map[*Conn]struct{})
		h.rooms[room] = rs
	}

	if h.maxRoomConns > 0 && len(rs) >= h.maxRoomConns {
		h.rejected.Add(1)
		if h.config.EnableSecurityMetrics {
			h.recordSecurityEvent("room_full", map[string]any{
				"room":  room,
				"count": len(rs),
			}, "warning")
		}
		return ErrRoomFull
	}

	rs[c] = struct{}{}
	h.roomRegistrations.Add(1) // Increment atomic counter
	h.accepted.Add(1)

	if h.config.EnableDebugLogging {
		h.logger.Printf("Connection joined room: %q (registrations: %d, room: %d)", room, h.roomRegistrations.Load(), len(rs))
	}

	return nil
}

// CanJoin checks if a room can accept another connection.
//
// This is useful for pre-checking capacity before attempting to join.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	if err := hub.CanJoin("chat-room"); err != nil {
//		// Room is full
//		return
//	}
//	// Room has capacity, proceed with join
func (h *Hub) CanJoin(room string) error {
	return h.canJoin(room, h.roomNameValidator())
}

func (h *Hub) canJoin(room string, validator RoomNameValidator) error {
	if h.stopped.Load() {
		return ErrHubStopped
	}
	if err := validateRoomName(room, validator); err != nil {
		return err
	}

	// Fast path: check atomic counter for room registrations
	if h.maxRoomRegistrations > 0 && int(h.roomRegistrations.Load()) >= h.maxRoomRegistrations {
		return ErrHubFull
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.maxRoomConns > 0 {
		if rs, ok := h.rooms[room]; ok && len(rs) >= h.maxRoomConns {
			return ErrRoomFull
		}
	}

	return nil
}

// Metrics returns a snapshot of hub metrics.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	metrics := hub.Metrics()
//	fmt.Printf("Active connections: %d\n", metrics.ActiveConnections)
func (h *Hub) Metrics() HubMetrics {
	h.mu.RLock()
	rooms := len(h.rooms)
	seen := make(map[*Conn]struct{})
	for _, rs := range h.rooms {
		for c := range rs {
			if c != nil && !c.IsClosed() {
				seen[c] = struct{}{}
			}
		}
	}
	h.mu.RUnlock()

	return HubMetrics{
		ActiveConnections:    len(seen),
		RoomRegistrations:    int(h.roomRegistrations.Load()),
		Rooms:                rooms,
		AcceptedTotal:        h.accepted.Load(),
		RejectedTotal:        h.rejected.Load(),
		MaxRoomRegistrations: h.maxRoomRegistrations,
		MaxRoomConnections:   h.maxRoomConns,
		BroadcastAttempted:   h.broadcastAttempted.Load(),
		BroadcastEnqueued:    h.broadcastEnqueued.Load(),
		BroadcastSkipped:     h.broadcastSkipped.Load(),
		BroadcastDropped:     h.broadcastDropped.Load(),
		SecurityRejections:   h.securityRejections.Load(),
		InvalidWSKeys:        h.invalidWSKeys.Load(),
		SuccessfulAuths:      h.successfulAuths.Load(),
	}
}

// Leave room.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	conn, err := websocket.NewConn(...)
//	hub.TryJoin("chat-room", conn)
//	// ... handle connection ...
//	hub.Leave("chat-room", conn)
func (h *Hub) Leave(room string, c *Conn) {
	if c == nil {
		return
	}
	if err := validateRoomName(room, h.roomNameValidator()); err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if rs, ok := h.rooms[room]; ok {
		if _, exists := rs[c]; exists {
			delete(rs, c)
			h.roomRegistrations.Add(^uint64(0)) // Decrement (add -1 in two's complement)
			if len(rs) == 0 {
				delete(h.rooms, room)
			}
		}
	}
}

// RemoveConn from all rooms.
//
// This is useful when a connection is closed and needs to be cleaned up from all rooms.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	conn, err := websocket.NewConn(...)
//	hub.TryJoin("chat-room", conn)
//	hub.TryJoin("notifications-room", conn)
//	// Connection closed, remove from all rooms
//	hub.RemoveConn(conn)
func (h *Hub) RemoveConn(c *Conn) {
	if c == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	removedCount := 0
	for room, rs := range h.rooms {
		if _, ok := rs[c]; ok {
			delete(rs, c)
			removedCount++
			if len(rs) == 0 {
				delete(h.rooms, room)
			}
		}
	}
	// Decrement roomRegistrations by the number of rooms the connection was in
	if removedCount > 0 {
		h.roomRegistrations.Add(^uint64(removedCount - 1)) // Subtract removedCount
	}
}

// GetRoomCount returns the number of connections in a room.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	count := hub.GetRoomCount("chat-room")
//	fmt.Printf("Chat room has %d connections\n", count)
func (h *Hub) GetRoomCount(room string) int {
	if err := validateRoomName(room, h.roomNameValidator()); err != nil {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if rs, ok := h.rooms[room]; ok {
		return len(rs)
	}
	return 0
}

// GetRoomRegistrationCount returns the number of connection-room registrations.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	total := hub.GetRoomRegistrationCount()
//	fmt.Printf("Room registrations: %d\n", total)
func (h *Hub) GetRoomRegistrationCount() int {
	return int(h.roomRegistrations.Load())
}

// GetActiveConnectionCount returns the number of unique open connections
// registered in at least one room.
func (h *Hub) GetActiveConnectionCount() int {
	return h.Metrics().ActiveConnections
}

// GetRooms returns a list of all room names.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	rooms := hub.GetRooms()
//	for _, room := range rooms {
//		fmt.Printf("Room: %s, Connections: %d\n", room, hub.GetRoomCount(room))
//	}
func (h *Hub) GetRooms() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	rooms := make([]string, 0, len(h.rooms))
	for room := range h.rooms {
		rooms = append(rooms, room)
	}
	return rooms
}
