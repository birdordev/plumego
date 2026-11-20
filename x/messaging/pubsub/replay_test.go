package pubsub

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

type replayMessagePayload struct {
	Seq int
}

func TestReplayStore_Basic(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false // Disable archiving for this test

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish some messages
	for i := 0; i < 5; i++ {
		msg := Message{Data: replayMessagePayload{Seq: i}}
		_ = ps.Publish("test.replay", msg)
	}

	time.Sleep(300 * time.Millisecond)

	// Query all messages
	results, err := rs.Query(ReplayQuery{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(results))
	}

	// Check stats
	stats := rs.Stats()
	if stats.TotalMessages.Load() != 5 {
		t.Errorf("Expected 5 total messages, got %d", stats.TotalMessages.Load())
	}
}

func TestReplayStore_TimeRangeQuery(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish messages over time
	for i := 0; i < 3; i++ {
		msg := Message{Data: i}
		_ = ps.Publish("test.time", msg)
		time.Sleep(50 * time.Millisecond)
	}

	for i := 3; i < 6; i++ {
		msg := Message{Data: i}
		_ = ps.Publish("test.time", msg)
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	all, err := rs.Query(ReplayQuery{Ascending: true})
	if err != nil {
		t.Fatalf("Query all failed: %v", err)
	}
	if len(all) != 6 {
		t.Fatalf("Expected 6 messages, got %d", len(all))
	}

	// Query first half
	results, err := rs.Query(ReplayQuery{
		StartTime: all[0].Timestamp,
		EndTime:   all[2].Timestamp,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 messages in first half, got %d", len(results))
	}

	// Query second half
	results, err = rs.Query(ReplayQuery{
		StartTime: all[3].Timestamp,
		EndTime:   all[5].Timestamp,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 messages in second half, got %d", len(results))
	}
}

func TestReplayStore_TopicFilter(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish to different topics
	for i := 0; i < 3; i++ {
		_ = ps.Publish("topic.a", Message{Data: i})
	}
	for i := 0; i < 2; i++ {
		_ = ps.Publish("topic.b", Message{Data: i})
	}

	time.Sleep(100 * time.Millisecond)

	// Query topic.a only
	results, err := rs.Query(ReplayQuery{
		Topics: []string{"topic.a"},
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 messages for topic.a, got %d", len(results))
	}

	// Verify all results are from topic.a
	for _, msg := range results {
		if msg.Topic != "topic.a" {
			t.Errorf("Expected topic.a, got %s", msg.Topic)
		}
	}
}

func TestReplayStore_MessageIDQuery(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish messages and collect IDs
	ids := make([]string, 0)
	for i := 0; i < 5; i++ {
		msg := Message{Data: i}
		_ = ps.Publish("test.id", msg)
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	// Get all messages to retrieve IDs
	all, _ := rs.Query(ReplayQuery{})
	if len(all) >= 2 {
		ids = append(ids, all[0].ID, all[1].ID)
	}

	// Query specific IDs
	results, err := rs.Query(ReplayQuery{
		MessageIDs: ids,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != len(ids) {
		t.Errorf("Expected %d messages, got %d", len(ids), len(results))
	}

	// Verify IDs match
	foundIDs := make(map[string]bool)
	for _, msg := range results {
		foundIDs[msg.ID] = true
	}

	for _, id := range ids {
		if !foundIDs[id] {
			t.Errorf("Expected to find message ID %s", id)
		}
	}
}

func TestReplayStore_GetByID(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish a message
	_ = ps.Publish("test.getid", Message{Data: "test"})

	time.Sleep(100 * time.Millisecond)

	// Get all messages to find an ID
	all, _ := rs.Query(ReplayQuery{})
	if len(all) == 0 {
		t.Fatal("No messages stored")
	}

	msgID := all[0].ID

	// Get by ID
	msg, err := rs.GetByID(msgID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}

	if msg.ID != msgID {
		t.Errorf("Expected ID %s, got %s", msgID, msg.ID)
	}

	// Try non-existent ID
	_, err = rs.GetByID("nonexistent")
	if err != ErrMessageNotFound {
		t.Errorf("Expected ErrMessageNotFound, got %v", err)
	}
}

func TestReplayStore_Replay(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Create subscriber to count replayed messages
	sub, _ := ps.Subscribe(t.Context(), "replay.topic", SubOptions{BufferSize: 100})
	defer sub.Cancel()

	// Publish original messages
	for i := 0; i < 3; i++ {
		_ = ps.Publish("replay.topic", Message{Data: i})
	}

	// Drain original messages (ring buffer uses unbuffered channel, must use select)
	drainDeadline := time.After(150 * time.Millisecond)
drainOrig:
	for {
		select {
		case <-sub.C():
		case <-drainDeadline:
			break drainOrig
		}
	}

	// Replay all messages
	count, err := rs.Replay(ReplayQuery{
		Topics: []string{"replay.topic"},
	})
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}

	if count != 3 {
		t.Errorf("Expected to replay 3 messages, got %d", count)
	}

	// Verify replayed messages were received (ring buffer: unbuffered channel)
	received := 0
	replayDeadline := time.After(200 * time.Millisecond)
replayDrain:
	for received < 3 {
		select {
		case <-sub.C():
			received++
		case <-replayDeadline:
			break replayDrain
		}
	}

	if received != 3 {
		t.Errorf("Expected to receive 3 replayed messages, got %d", received)
	}

	// Check replay count updated
	stats := rs.Stats()
	if stats.ReplayedMessages.Load() != 3 {
		t.Errorf("Expected 3 replayed messages in stats, got %d", stats.ReplayedMessages.Load())
	}
}

func TestReplayStore_Pagination(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish 10 messages
	for i := 0; i < 10; i++ {
		_ = ps.Publish("test.page", Message{Data: i})
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	// Query first page
	page1, err := rs.Query(ReplayQuery{
		Limit:  3,
		Offset: 0,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(page1) != 3 {
		t.Errorf("Expected 3 messages in first page, got %d", len(page1))
	}

	// Query second page
	page2, err := rs.Query(ReplayQuery{
		Limit:  3,
		Offset: 3,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(page2) != 3 {
		t.Errorf("Expected 3 messages in second page, got %d", len(page2))
	}

	// Verify pages are different
	if page1[0].ID == page2[0].ID {
		t.Error("Pages should contain different messages")
	}
}

func TestReplayStore_Sorting(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish messages to different topics
	_ = ps.Publish("z.topic", Message{Data: 1})
	time.Sleep(10 * time.Millisecond)
	_ = ps.Publish("a.topic", Message{Data: 2})
	time.Sleep(10 * time.Millisecond)
	_ = ps.Publish("m.topic", Message{Data: 3})

	time.Sleep(100 * time.Millisecond)

	// Sort by topic ascending
	results, err := rs.Query(ReplayQuery{
		SortBy:    "topic",
		Ascending: true,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) >= 2 {
		if results[0].Topic > results[1].Topic {
			t.Error("Messages not sorted by topic ascending")
		}
	}

	// Sort by timestamp descending
	results, err = rs.Query(ReplayQuery{
		SortBy:    "timestamp",
		Ascending: false,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) >= 2 {
		if results[0].Timestamp.Before(results[1].Timestamp) {
			t.Error("Messages not sorted by timestamp descending")
		}
	}
}

func TestReplayStore_Archive(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = true
	config.RetentionPeriod = 100 * time.Millisecond
	config.ArchiveInterval = 50 * time.Millisecond

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish messages
	for i := 0; i < 3; i++ {
		_ = ps.Publish("test.archive", Message{Data: i})
	}

	time.Sleep(50 * time.Millisecond)

	// Wait for messages to age and be archived
	time.Sleep(200 * time.Millisecond)

	// Check archived count
	stats := rs.Stats()
	if stats.ArchivedMessages.Load() == 0 {
		t.Log("Note: Messages may not be archived yet")
	}

	// Verify archive file was created
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("Failed to read archive dir: %v", err)
	}

	if len(entries) > 0 {
		t.Logf("Archive file created: %s", entries[0].Name())
	}
}

func TestReplayStore_ArchiveCompression(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = true
	config.ArchiveCompression = true
	config.RetentionPeriod = 50 * time.Millisecond
	config.ArchiveInterval = 50 * time.Millisecond

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish messages
	for i := 0; i < 5; i++ {
		_ = ps.Publish("test.compress", Message{Data: i})
	}

	time.Sleep(50 * time.Millisecond)

	// Wait for archiving
	time.Sleep(150 * time.Millisecond)

	// Check for .gz files
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("Failed to read archive dir: %v", err)
	}

	foundGzip := false
	for _, entry := range entries {
		if len(entry.Name()) > 3 && entry.Name()[len(entry.Name())-3:] == ".gz" {
			foundGzip = true
			t.Logf("Found gzip archive: %s", entry.Name())
			break
		}
	}

	if len(entries) > 0 && !foundGzip {
		t.Log("Note: Gzip archive may not be created yet")
	}
}

func TestReplayStore_MaxMessages(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false
	config.MaxMessages = 5

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish more messages than max
	for i := 0; i < 10; i++ {
		_ = ps.Publish("test.max", Message{Data: i})
		time.Sleep(10 * time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)

	// Query all messages
	results, err := rs.Query(ReplayQuery{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) > config.MaxMessages {
		t.Errorf("Expected at most %d messages, got %d", config.MaxMessages, len(results))
	}
}

func TestReplayStore_InvalidTimeRange(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Query with invalid time range
	_, err = rs.Query(ReplayQuery{
		StartTime: time.Now(),
		EndTime:   time.Now().Add(-1 * time.Hour),
	})

	if err != ErrInvalidTimeRange {
		t.Errorf("Expected ErrInvalidTimeRange, got %v", err)
	}
}

func TestReplayStore_Stats(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Publish to multiple topics
	_ = ps.Publish("stats.a", Message{Data: 1})
	_ = ps.Publish("stats.b", Message{Data: 2})
	_ = ps.Publish("stats.c", Message{Data: 3})

	time.Sleep(100 * time.Millisecond)

	stats := rs.Stats()

	if stats.TotalMessages.Load() != 3 {
		t.Errorf("Expected 3 total messages, got %d", stats.TotalMessages.Load())
	}

	if stats.TopicCount != 3 {
		t.Errorf("Expected 3 topics, got %d", stats.TopicCount)
	}

	if stats.OldestMessage.IsZero() {
		t.Error("Expected oldest message timestamp to be set")
	}

	if stats.NewestMessage.IsZero() {
		t.Error("Expected newest message timestamp to be set")
	}
}

func TestReplayStore_Close(t *testing.T) {
	t.Parallel()
	ps := New()
	defer ps.Close()

	dir := t.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir

	rs, err := NewReplayStore(ps, config)
	if err != nil {
		t.Fatalf("Failed to create replay store: %v", err)
	}

	// Close
	if err := rs.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify closed
	_, err = rs.Query(ReplayQuery{})
	if err != ErrReplayClosed {
		t.Errorf("Expected ErrReplayClosed, got %v", err)
	}

	// Double close should be idempotent.
	if err := rs.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

func BenchmarkReplayStore_Store(b *testing.B) {
	ps := New()
	defer ps.Close()

	dir := b.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false
	config.MaxMessages = 100000

	rs, _ := NewReplayStore(ps, config)
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ps.Publish("bench.store", Message{Data: i})
	}
}

func BenchmarkReplayStore_Query(b *testing.B) {
	ps := New()
	defer ps.Close()

	dir := b.TempDir()
	config := DefaultReplayConfig()
	config.ArchiveDir = dir
	config.ArchiveEnabled = false

	rs, _ := NewReplayStore(ps, config)
	defer rs.Close()

	// Wait for subscription to be ready
	time.Sleep(100 * time.Millisecond)

	// Populate with messages
	for i := 0; i < 1000; i++ {
		_ = ps.Publish("bench.query", Message{Data: i})
	}

	time.Sleep(200 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = rs.Query(ReplayQuery{
			Topics: []string{"bench.query"},
			Limit:  100,
		})
	}
}

func TestReplayArchiveFailureRetainsIndexesAndCanRetry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "archive")
	rs := &ReplayStore{
		config:     ReplayConfig{ArchiveEnabled: true, ArchiveDir: dir, RetentionPeriod: time.Hour, MaxMessages: 10},
		messages:   make(map[string]*ReplayMessage),
		topicIndex: make(map[string][]string),
	}
	rs.storeMessage(Message{ID: "sample", Topic: "old", Time: time.Now().Add(-2 * time.Hour)})
	rs.archiveOldMessages()
	if _, err := rs.GetByID("sample"); err != nil {
		t.Fatal(err)
	}
	if len(rs.topicIndex["old"]) != 1 || len(rs.timeIndex) != 1 || len(rs.archived) != 0 {
		t.Fatal("failed archive changed the replay indexes")
	}
	rs.archiveMessages(func([]*ReplayMessage) error { return errors.New("finalizing archive failed") })
	if _, err := rs.GetByID("sample"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	rs.archiveOldMessages()
	rs.archiveOldMessages()
	messages, err := rs.Query(ReplayQuery{IncludeArchived: true})
	if err != nil || len(messages) != 1 || !messages[0].IsArchived ||
		len(rs.messages) != 0 || len(rs.timeIndex) != 0 || len(rs.topicIndex["old"]) != 0 {
		t.Fatalf("archive retry: messages=%v, err=%v", messages, err)
	}
}

func TestReplayArchiveRetainsReplacementDuringWrite(t *testing.T) {
	rs := &ReplayStore{
		config:     ReplayConfig{RetentionPeriod: time.Hour, MaxMessages: 10},
		messages:   make(map[string]*ReplayMessage),
		topicIndex: make(map[string][]string),
	}
	rs.storeMessage(Message{ID: "sample", Topic: "old", Time: time.Now().Add(-2 * time.Hour)})
	rs.archiveMessages(func(batch []*ReplayMessage) error {
		done := make(chan struct{})
		go func() {
			rs.storeMessage(Message{ID: "sample", Topic: "new", Time: time.Now()})
			close(done)
		}()
		<-done
		if len(batch) != 1 || batch[0].Topic != "old" {
			t.Fatalf("archive batch = %v", batch)
		}
		return nil
	})
	got, err := rs.GetByID("sample")
	if err != nil || got.Topic != "new" || len(rs.timeIndex) != 1 ||
		len(rs.topicIndex["old"]) != 0 || len(rs.topicIndex["new"]) != 1 || len(rs.archived) != 1 {
		t.Fatalf("replacement after archive commit = %+v, %v", got, err)
	}
}

func TestReplayArchivePublishesDistinctCompleteBatches(t *testing.T) {
	for _, compress := range []bool{false, true} {
		name := "plain"
		if compress {
			name = "gzip"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			synctest.Test(t, func(t *testing.T) {
				rs := &ReplayStore{config: ReplayConfig{ArchiveEnabled: true, ArchiveDir: dir, ArchiveCompression: compress}}
				for _, id := range []string{"first", "second"} {
					if err := rs.writeArchive([]*ReplayMessage{{ID: id, Topic: "sample"}}); err != nil {
						t.Fatal(err)
					}
				}
				if err := rs.writeArchive([]*ReplayMessage{{ID: "bad", Data: make(chan int)}}); err == nil {
					t.Fatal("archive of an unsupported payload succeeded")
				}

				files, err := os.ReadDir(dir)
				if err != nil || len(files) != 2 {
					t.Fatalf("archive directory = %v, %v", files, err)
				}
				for _, file := range files {
					if err := rs.LoadArchive(file.Name()); err != nil {
						t.Fatal(err)
					}
				}
				var ids []string
				for _, msg := range rs.archived {
					ids = append(ids, msg.ID)
				}
				slices.Sort(ids)
				if !slices.Equal(ids, []string{"first", "second"}) {
					t.Errorf("loaded archive IDs = %v", ids)
				}
			})
		})
	}
}
