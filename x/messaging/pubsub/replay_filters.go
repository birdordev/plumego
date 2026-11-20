package pubsub

import (
	"maps"
	"sort"
)

// Query returns snapshots of messages matching the query. Metadata and common
// payload containers are copied. Custom payloads follow the Message ownership
// rules and can provide a Cloner or DeepCopier.
func (rs *ReplayStore) Query(query ReplayQuery) ([]*ReplayMessage, error) {
	if rs.closed.Load() {
		return nil, ErrReplayClosed
	}

	// Validate time range
	if !query.StartTime.IsZero() && !query.EndTime.IsZero() {
		if query.EndTime.Before(query.StartTime) {
			return nil, ErrInvalidTimeRange
		}
	}

	results := make([]*ReplayMessage, 0)

	// Query in-memory messages
	rs.messagesMu.RLock()
	for _, msg := range rs.messages {
		if rs.matchesQuery(msg, query) {
			msgCopy := *msg
			results = append(results, &msgCopy)
		}
	}

	// Include archived if requested
	if query.IncludeArchived {
		rs.archivedMu.RLock()
		for _, msg := range rs.archived {
			if rs.matchesQuery(msg, query) {
				msgCopy := *msg
				results = append(results, &msgCopy)
			}
		}
		rs.archivedMu.RUnlock()
	}

	rs.messagesMu.RUnlock()

	// Sort results
	rs.sortMessages(results, query.SortBy, query.Ascending)

	// Apply offset and limit
	if query.Offset > 0 {
		if query.Offset >= len(results) {
			return []*ReplayMessage{}, nil
		}
		results = results[query.Offset:]
	}

	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	for _, msg := range results {
		copyReplayPayload(msg)
	}
	return results, nil
}

// matchesQuery checks if a message matches the query filters.
func (rs *ReplayStore) matchesQuery(msg *ReplayMessage, query ReplayQuery) bool {
	// Time range filter
	if !query.StartTime.IsZero() && msg.Timestamp.Before(query.StartTime) {
		return false
	}
	if !query.EndTime.IsZero() && msg.Timestamp.After(query.EndTime) {
		return false
	}

	// Topic filter
	if len(query.Topics) > 0 {
		found := false
		for _, topic := range query.Topics {
			if msg.Topic == topic {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Message ID filter
	if len(query.MessageIDs) > 0 {
		found := false
		for _, id := range query.MessageIDs {
			if msg.ID == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// sortMessages sorts messages by the specified field.
func (rs *ReplayStore) sortMessages(messages []*ReplayMessage, sortBy string, ascending bool) {
	switch sortBy {
	case "topic":
		sort.Slice(messages, func(i, j int) bool {
			if ascending {
				return messages[i].Topic < messages[j].Topic
			}
			return messages[i].Topic > messages[j].Topic
		})
	case "size":
		sort.Slice(messages, func(i, j int) bool {
			if ascending {
				return messages[i].Size < messages[j].Size
			}
			return messages[i].Size > messages[j].Size
		})
	default: // timestamp
		sort.Slice(messages, func(i, j int) bool {
			if ascending {
				return messages[i].Timestamp.Before(messages[j].Timestamp)
			}
			return messages[i].Timestamp.After(messages[j].Timestamp)
		})
	}
}

// GetByID retrieves a snapshot of an in-memory message by ID. Payload
// ownership follows the rules documented for Query.
func (rs *ReplayStore) GetByID(messageID string) (*ReplayMessage, error) {
	if rs.closed.Load() {
		return nil, ErrReplayClosed
	}

	rs.messagesMu.RLock()
	msg, exists := rs.messages[messageID]
	if !exists {
		rs.messagesMu.RUnlock()
		return nil, ErrMessageNotFound
	}
	msgCopy := *msg
	rs.messagesMu.RUnlock()

	copyReplayPayload(&msgCopy)
	return &msgCopy, nil
}

// copyReplayPayload operates on a detached snapshot. Custom copiers can call
// back into the store, so callers must release the replay locks first.
func copyReplayPayload(msg *ReplayMessage) {
	msg.Metadata = maps.Clone(msg.Metadata)
	msg.Data = cloneData(msg.Data)
}
