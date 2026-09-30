package pubsub

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// storeMessage stores a message for replay.
func (rs *ReplayStore) storeMessage(msg Message) {
	if rs.closed.Load() {
		return
	}

	// Generate ID if missing
	msgID := msg.ID
	if msgID == "" {
		msgID = fmt.Sprintf("%s-%d-%d", msg.Topic, msg.Time.UnixNano(), rs.stats.TotalMessages.Load())
	}

	// Check size limit
	rs.messagesMu.Lock()
	if len(rs.messages) >= rs.config.MaxMessages {
		// Remove oldest message
		rs.removeOldest()
	}

	// Create replay message
	replayMsg := &ReplayMessage{
		ID:        msgID,
		Topic:     msg.Topic,
		Timestamp: msg.Time,
		Metadata:  make(map[string]string),
	}

	if rs.config.IncludeData {
		replayMsg.Data = msg.Data
	}

	// Estimate size
	if data, err := json.Marshal(msg.Data); err == nil {
		replayMsg.Size = len(data)
	}

	// Store message
	rs.messages[msgID] = replayMsg
	rs.messagesMu.Unlock()

	// Update topic index
	rs.topicIndexMu.Lock()
	rs.topicIndex[msg.Topic] = append(rs.topicIndex[msg.Topic], msgID)
	rs.topicIndexMu.Unlock()

	// Update time index: binary search insertion maintains sorted order in O(log n)
	// rather than re-sorting the whole slice O(n log n) on every message.
	rs.timeIndexMu.Lock()
	pos := sort.Search(len(rs.timeIndex), func(i int) bool {
		return !rs.timeIndex[i].Timestamp.Before(replayMsg.Timestamp)
	})
	rs.timeIndex = append(rs.timeIndex, nil)
	copy(rs.timeIndex[pos+1:], rs.timeIndex[pos:])
	rs.timeIndex[pos] = replayMsg
	rs.timeIndexMu.Unlock()

	// Update stats
	rs.stats.TotalMessages.Add(1)
	rs.stats.TotalSize.Add(uint64(replayMsg.Size))
}

// removeOldest removes the oldest message from storage.
func (rs *ReplayStore) removeOldest() {
	if len(rs.timeIndex) == 0 {
		return
	}

	// Get oldest from time index
	oldest := rs.timeIndex[0]

	// Remove from messages map
	delete(rs.messages, oldest.ID)

	// Remove from topic index
	rs.topicIndexMu.Lock()
	if ids, ok := rs.topicIndex[oldest.Topic]; ok {
		for i, id := range ids {
			if id == oldest.ID {
				rs.topicIndex[oldest.Topic] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
	}
	rs.topicIndexMu.Unlock()

	// Remove from time index
	rs.timeIndexMu.Lock()
	rs.timeIndex = rs.timeIndex[1:]
	rs.timeIndexMu.Unlock()
}

// archiveWorker periodically archives old messages.
func (rs *ReplayStore) archiveWorker() {
	defer rs.wg.Done()

	if !rs.config.ArchiveEnabled {
		return
	}

	ticker := time.NewTicker(rs.config.ArchiveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rs.archiveOldMessages()
		case <-rs.ctx.Done():
			return
		}
	}
}

// archiveOldMessages moves old messages to archive.
// Locks are acquired sequentially (never two at once) to avoid deadlock with
// storeMessage->removeOldest which acquires messagesMu->topicIndexMu->timeIndexMu
// in the same sequential fashion.
func (rs *ReplayStore) archiveOldMessages() {
	cutoff := time.Now().Add(-rs.config.RetentionPeriod)

	// Phase 1: identify and remove expired entries from the time index.
	// timeIndex is sorted ascending, so all expired entries form a prefix.
	rs.timeIndexMu.Lock()
	cutoffIdx := 0
	for cutoffIdx < len(rs.timeIndex) && rs.timeIndex[cutoffIdx].Timestamp.Before(cutoff) {
		cutoffIdx++
	}
	toArchive := make([]*ReplayMessage, cutoffIdx)
	copy(toArchive, rs.timeIndex[:cutoffIdx])
	rs.timeIndex = rs.timeIndex[cutoffIdx:]
	rs.timeIndexMu.Unlock()

	if len(toArchive) == 0 {
		return
	}

	// Phase 2: remove from messages map.
	rs.messagesMu.Lock()
	for _, msg := range toArchive {
		delete(rs.messages, msg.ID)
	}
	rs.messagesMu.Unlock()

	// Phase 3: remove from topic index.
	rs.topicIndexMu.Lock()
	for _, msg := range toArchive {
		if ids, ok := rs.topicIndex[msg.Topic]; ok {
			for j, id := range ids {
				if id == msg.ID {
					rs.topicIndex[msg.Topic] = append(ids[:j], ids[j+1:]...)
					break
				}
			}
		}
	}
	rs.topicIndexMu.Unlock()

	// Write to archive file
	if err := rs.writeArchive(toArchive); err != nil {
		// Log error but continue
		return
	}

	// Move to archived list
	rs.archivedMu.Lock()
	for _, msg := range toArchive {
		msg.IsArchived = true
		rs.archived = append(rs.archived, msg)
		rs.stats.ArchivedMessages.Add(1)
	}
	rs.archivedMu.Unlock()
}

// writeArchive writes messages to archive file.
func (rs *ReplayStore) writeArchive(messages []*ReplayMessage) error {
	if len(messages) == 0 {
		return nil
	}

	// Create filename with timestamp
	filename := fmt.Sprintf("archive-%s.json", time.Now().Format("2006-01-02-150405"))
	if rs.config.ArchiveCompression {
		filename += ".gz"
	}

	filepath := filepath.Join(rs.config.ArchiveDir, filename)

	file, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	var writer io.Writer = file

	// Add gzip compression if enabled
	if rs.config.ArchiveCompression {
		gzWriter := gzip.NewWriter(file)
		defer func() { _ = gzWriter.Close() }()
		writer = gzWriter
	}

	// Write messages as JSON lines
	encoder := json.NewEncoder(writer)
	for _, msg := range messages {
		if err := encoder.Encode(msg); err != nil {
			return err
		}
	}

	return nil
}

// LoadArchive loads messages from an archive file.
func (rs *ReplayStore) LoadArchive(filename string) error {
	if !rs.config.ArchiveEnabled {
		return ErrArchiveNotEnabled
	}

	filepath := filepath.Join(rs.config.ArchiveDir, filename)

	file, err := os.Open(filepath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	var reader io.Reader = file

	// Detect gzip compression
	if filepath[len(filepath)-3:] == ".gz" {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer func() { _ = gzReader.Close() }()
		reader = gzReader
	}

	// Read messages
	decoder := json.NewDecoder(reader)
	loaded := 0

	rs.archivedMu.Lock()
	defer rs.archivedMu.Unlock()

	for {
		var msg ReplayMessage
		if err := decoder.Decode(&msg); err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		msg.IsArchived = true
		rs.archived = append(rs.archived, &msg)
		loaded++
	}

	return nil
}
