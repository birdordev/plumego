package websocket

import "slices"

// dispatchJobs enqueues send jobs for each connection in conns and tracks drops.
// label is used only for log/metric messages to identify the broadcast target.
func (h *Hub) dispatchJobs(conns []*Conn, op byte, data []byte, label string) BroadcastResult {
	result := BroadcastResult{Attempted: len(conns)}
	h.lifecycleMu.RLock()
	defer h.lifecycleMu.RUnlock()
	if h.stopped.Load() {
		result.Dropped = len(conns)
		result.Stopped = true
		h.broadcastAttempted.Add(uint64(result.Attempted))
		h.broadcastDropped.Add(uint64(result.Dropped))
		return result
	}
	if result.Attempted == 0 {
		return result
	}
	// Jobs share one immutable snapshot of the caller's payload.
	payload := slices.Clone(data)
	for _, c := range conns {
		if c == nil || c.IsClosed() {
			result.Skipped++
			continue
		}
		select {
		case h.jobQueue <- hubJob{conn: c, op: op, data: payload}:
			result.Enqueued++
		default:
			result.Dropped++
			if h.config.RejectOnQueueFull {
				if h.config.EnableDebugLogging {
					h.logger.Printf("broadcast queue full: dropped message to %q", label)
				}
				if h.config.EnableSecurityMetrics {
					h.recordSecurityEvent("broadcast_queue_full", map[string]any{
						"target":  label,
						"dropped": result.Dropped,
						"sent":    result.Enqueued,
					}, "error")
				}
			}
		}
	}
	h.broadcastAttempted.Add(uint64(result.Attempted))
	h.broadcastEnqueued.Add(uint64(result.Enqueued))
	h.broadcastSkipped.Add(uint64(result.Skipped))
	h.broadcastDropped.Add(uint64(result.Dropped))
	return result
}

// BroadcastRoom enqueues jobs to jobQueue for workers to send.
//
// This method is thread-safe and can be called concurrently from multiple goroutines.
// Messages are delivered asynchronously via the worker pool.
// The caller can reuse data after BroadcastRoom returns.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	// Broadcast text message to all users in chat-room
//	hub.BroadcastRoom("chat-room", websocket.OpcodeText, []byte("Hello everyone!"))
//	// Broadcast binary data
//	hub.BroadcastRoom("chat-room", websocket.OpcodeBinary, []byte{0x01, 0x02, 0x03})
func (h *Hub) BroadcastRoom(room string, op byte, data []byte) {
	_ = h.TryBroadcastRoom(room, op, data)
}

// TryBroadcastRoom enqueues jobs for room members and returns the fanout result.
// The caller can reuse data after this method returns.
func (h *Hub) TryBroadcastRoom(room string, op byte, data []byte) BroadcastResult {
	return h.tryBroadcastRoom(room, op, data, h.roomNameValidator())
}

func (h *Hub) tryBroadcastRoom(room string, op byte, data []byte, validator RoomNameValidator) BroadcastResult {
	if h.stopped.Load() {
		return BroadcastResult{Stopped: true}
	}
	if err := validateRoomName(room, validator); err != nil {
		return BroadcastResult{Invalid: true}
	}

	connsList := h.getConnList()
	defer h.putConnList(connsList)

	h.mu.RLock()
	rs, ok := h.rooms[room]
	if !ok || len(rs) == 0 {
		h.mu.RUnlock()
		return BroadcastResult{}
	}
	if cap(*connsList) < len(rs) {
		*connsList = make([]*Conn, 0, len(rs))
	}
	for c := range rs {
		*connsList = append(*connsList, c)
	}
	h.mu.RUnlock()

	return h.dispatchJobs(*connsList, op, data, "room:"+room)
}

// BroadcastAll broadcasts to all clients.
// The caller can reuse data after BroadcastAll returns.
//
// This is useful for system-wide announcements or notifications.
// Use with caution as it can generate significant network traffic.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	// Send system-wide notification
//	hub.BroadcastAll(websocket.OpcodeText, []byte("System maintenance in 5 minutes"))
func (h *Hub) BroadcastAll(op byte, data []byte) {
	_ = h.TryBroadcastAll(op, data)
}

// TryBroadcastAll enqueues jobs for all unique open connections and returns the fanout result.
// The caller can reuse data after this method returns.
func (h *Hub) TryBroadcastAll(op byte, data []byte) BroadcastResult {
	if h.stopped.Load() {
		return BroadcastResult{Stopped: true}
	}

	connsList := h.getConnList()
	defer h.putConnList(connsList)

	h.mu.RLock()
	// Fast path: single room needs no deduplication map.
	if len(h.rooms) == 1 {
		for _, rs := range h.rooms {
			if cap(*connsList) < len(rs) {
				*connsList = make([]*Conn, 0, len(rs))
			}
			for c := range rs {
				*connsList = append(*connsList, c)
			}
		}
		h.mu.RUnlock()
		return h.dispatchJobs(*connsList, op, data, "all")
	}

	// Multi-room path: deduplicate connections that span multiple rooms.
	hint := min(h.roomRegistrations.Load(), maxPooledConnections)
	seen := make(map[*Conn]struct{}, int(hint))
	for _, rs := range h.rooms {
		for c := range rs {
			if _, dup := seen[c]; !dup {
				seen[c] = struct{}{}
				*connsList = append(*connsList, c)
			}
		}
	}
	h.mu.RUnlock()

	return h.dispatchJobs(*connsList, op, data, "all")
}
