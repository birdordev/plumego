package websocket

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
	for _, c := range conns {
		if c == nil || c.IsClosed() {
			result.Skipped++
			continue
		}
		select {
		case h.jobQueue <- hubJob{conn: c, op: op, data: data}:
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

func (h *Hub) recordSkippedBroadcast(skipped int) BroadcastResult {
	result := BroadcastResult{Skipped: skipped}
	if skipped > 0 {
		h.broadcastSkipped.Add(uint64(skipped))
	}
	return result
}

// BroadcastRoom enqueues jobs to jobQueue for workers to send.
//
// This method is thread-safe and can be called concurrently from multiple goroutines.
// Messages are delivered asynchronously via the worker pool.
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
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
		if c != nil && !c.IsClosed() {
			*connsList = append(*connsList, c)
		}
	}
	skipped := len(rs) - len(*connsList)
	h.mu.RUnlock()

	if len(*connsList) == 0 {
		return h.recordSkippedBroadcast(skipped)
	}
	result := h.dispatchJobs(*connsList, op, data, "room:"+room)
	result.Skipped += skipped
	if skipped > 0 {
		h.broadcastSkipped.Add(uint64(skipped))
	}
	return result
}

// BroadcastAll broadcasts to all clients.
//
// This is useful for system-wide announcements or notifications.
// Use with caution as it can generate significant network traffic.
//
// Example:
//
//	import "github.com/spcent/plumego/x/websocket"
//
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{WorkerCount: 4, JobQueueSize: 1024})
//	// Send system-wide notification
//	hub.BroadcastAll(websocket.OpcodeText, []byte("System maintenance in 5 minutes"))
func (h *Hub) BroadcastAll(op byte, data []byte) {
	_ = h.TryBroadcastAll(op, data)
}

// TryBroadcastAll enqueues jobs for all unique open connections and returns the fanout result.
func (h *Hub) TryBroadcastAll(op byte, data []byte) BroadcastResult {
	if h.stopped.Load() {
		return BroadcastResult{Stopped: true}
	}

	connsList := h.getConnList()
	defer h.putConnList(connsList)

	h.mu.RLock()
	// Fast path: single room needs no deduplication map.
	if len(h.rooms) == 1 {
		total := 0
		for _, rs := range h.rooms {
			total += len(rs)
			if cap(*connsList) < len(rs) {
				*connsList = make([]*Conn, 0, len(rs))
			}
			for c := range rs {
				if c != nil && !c.IsClosed() {
					*connsList = append(*connsList, c)
				}
			}
		}
		skipped := total - len(*connsList)
		h.mu.RUnlock()
		if len(*connsList) > 0 {
			result := h.dispatchJobs(*connsList, op, data, "all")
			result.Skipped += skipped
			if skipped > 0 {
				h.broadcastSkipped.Add(uint64(skipped))
			}
			return result
		}
		return h.recordSkippedBroadcast(skipped)
	}

	// Multi-room path: deduplicate connections that span multiple rooms.
	estimatedSize := 0
	for _, rs := range h.rooms {
		estimatedSize += len(rs)
	}
	if cap(*connsList) < estimatedSize {
		*connsList = make([]*Conn, 0, estimatedSize)
	}
	skipped := 0
	seen := make(map[*Conn]struct{}, estimatedSize)
	for _, rs := range h.rooms {
		for c := range rs {
			if c == nil || c.IsClosed() {
				skipped++
				continue
			}
			if _, dup := seen[c]; !dup {
				seen[c] = struct{}{}
				*connsList = append(*connsList, c)
			}
		}
	}
	h.mu.RUnlock()

	if len(*connsList) == 0 {
		return h.recordSkippedBroadcast(skipped)
	}
	result := h.dispatchJobs(*connsList, op, data, "all")
	result.Skipped += skipped
	if skipped > 0 {
		h.broadcastSkipped.Add(uint64(skipped))
	}
	return result
}
