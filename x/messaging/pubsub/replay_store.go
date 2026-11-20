package pubsub

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// storeMessage stores a message for replay.
func (rs *ReplayStore) storeMessage(msg Message) {
	if rs.closed.Load() {
		return
	}

	replayMsg := &ReplayMessage{
		ID:        msg.ID,
		Topic:     msg.Topic,
		Timestamp: msg.Time,
		Metadata:  make(map[string]string),
	}
	if rs.config.IncludeData {
		replayMsg.Data = msg.Data
	}
	if data, err := json.Marshal(msg.Data); err == nil {
		replayMsg.Size = len(data)
	}

	rs.messagesMu.Lock()
	defer rs.messagesMu.Unlock()
	if replayMsg.ID == "" {
		replayMsg.ID = fmt.Sprintf("%s-%d-%d", msg.Topic, msg.Time.UnixNano(), rs.stats.TotalMessages.Load())
	}
	if previous := rs.messages[replayMsg.ID]; previous != nil {
		rs.removeMessageLocked(previous)
		rs.timeIndex = slices.DeleteFunc(rs.timeIndex, func(msg *ReplayMessage) bool { return msg == previous })
	} else if len(rs.messages) >= rs.config.MaxMessages && len(rs.timeIndex) > 0 {
		rs.removeMessageLocked(rs.timeIndex[0])
		rs.timeIndex = slices.Delete(rs.timeIndex, 0, 1)
	}

	rs.messages[replayMsg.ID] = replayMsg
	rs.topicIndex[msg.Topic] = append(rs.topicIndex[msg.Topic], replayMsg.ID)
	pos := sort.Search(len(rs.timeIndex), func(i int) bool {
		return !rs.timeIndex[i].Timestamp.Before(replayMsg.Timestamp)
	})
	rs.timeIndex = slices.Insert(rs.timeIndex, pos, replayMsg)

	rs.stats.TotalMessages.Add(1)
	rs.stats.TotalSize.Add(uint64(replayMsg.Size))
}

// removeMessageLocked removes a message from the map and topic index.
func (rs *ReplayStore) removeMessageLocked(msg *ReplayMessage) {
	delete(rs.messages, msg.ID)
	ids := rs.topicIndex[msg.Topic]
	if i := slices.Index(ids, msg.ID); i >= 0 {
		rs.topicIndex[msg.Topic] = slices.Delete(ids, i, i+1)
	}
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

// archiveOldMessages moves old messages to archive after the file is complete.
func (rs *ReplayStore) archiveOldMessages() {
	rs.archiveMessages(rs.writeArchive)
}

func (rs *ReplayStore) archiveMessages(write func([]*ReplayMessage) error) {
	cutoff := time.Now().Add(-rs.config.RetentionPeriod)
	rs.messagesMu.RLock()
	limit := sort.Search(len(rs.timeIndex), func(i int) bool {
		return !rs.timeIndex[i].Timestamp.Before(cutoff)
	})
	sources := slices.Clone(rs.timeIndex[:limit])
	batch := make([]*ReplayMessage, 0, limit)
	for _, source := range sources {
		snapshot := *source
		batch = append(batch, &snapshot)
	}
	rs.messagesMu.RUnlock()

	if len(batch) == 0 || write(batch) != nil {
		return
	}

	rs.messagesMu.Lock()
	defer rs.messagesMu.Unlock()
	rs.archivedMu.Lock()
	defer rs.archivedMu.Unlock()
	moved := make(map[*ReplayMessage]struct{}, len(sources))
	for i, source := range sources {
		if rs.messages[source.ID] == source {
			rs.removeMessageLocked(source)
			moved[source] = struct{}{}
		}
		batch[i].IsArchived = true
	}
	rs.timeIndex = slices.DeleteFunc(rs.timeIndex, func(msg *ReplayMessage) bool {
		_, ok := moved[msg]
		return ok
	})
	rs.archived = append(rs.archived, batch...)
	rs.stats.ArchivedMessages.Add(uint64(len(batch)))
}

// writeArchive writes messages to archive file.
func (rs *ReplayStore) writeArchive(messages []*ReplayMessage) error {
	if len(messages) == 0 {
		return nil
	}

	prefix := ".archive-" + time.Now().UTC().Format("20060102-150405") + "-"
	file, err := os.CreateTemp(rs.config.ArchiveDir, prefix)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()

	err = writeArchiveRecords(file, messages, rs.config.ArchiveCompression)
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}

	name := strings.TrimPrefix(filepath.Base(file.Name()), ".") + ".json"
	if rs.config.ArchiveCompression {
		name += ".gz"
	}
	// Publish the complete file without replacing an existing archive.
	return os.Link(file.Name(), filepath.Join(rs.config.ArchiveDir, name))
}

func writeArchiveRecords(w io.Writer, messages []*ReplayMessage, compress bool) error {
	if !compress {
		return encodeArchiveRecords(w, messages)
	}
	gz := gzip.NewWriter(w)
	err := encodeArchiveRecords(gz, messages)
	return errors.Join(err, gz.Close())
}

func encodeArchiveRecords(w io.Writer, messages []*ReplayMessage) error {
	encoder := json.NewEncoder(w)
	for _, msg := range messages {
		if err := encoder.Encode(msg); err != nil {
			return err
		}
	}
	return nil
}

// LoadArchive loads JSON Lines from an archive file, optionally compressed
// with gzip. It appends the batch only after decoding to the end of the file.
// Files created by older versions remain readable.
func (rs *ReplayStore) LoadArchive(filename string) error {
	if !rs.config.ArchiveEnabled {
		return ErrArchiveNotEnabled
	}

	path := filepath.Join(rs.config.ArchiveDir, filename)

	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	var reader io.Reader = file

	// Detect gzip compression
	if strings.HasSuffix(path, ".gz") {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer func() { _ = gzReader.Close() }()
		reader = gzReader
	}

	// Read messages
	decoder := json.NewDecoder(reader)
	var loaded []*ReplayMessage

	for {
		var msg ReplayMessage
		if err := decoder.Decode(&msg); err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		msg.IsArchived = true
		loaded = append(loaded, &msg)
	}

	rs.archivedMu.Lock()
	rs.archived = append(rs.archived, loaded...)
	rs.archivedMu.Unlock()
	return nil
}
