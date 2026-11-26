package cmdb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
	"time"

	"with-cloud-manager/internal/inventory"
)

type SnapshotWriter struct {
	output  io.Writer
	digest  hash.Hash
	project Project
	count   int
	bytes   int
}

func NewSnapshotWriter(output io.Writer, project Project) *SnapshotWriter {
	return &SnapshotWriter{output: output, digest: sha256.New(), project: project}
}

func (s *SnapshotWriter) Start(capturedAt time.Time) error {
	return s.write(record{Type: "manifest", Manifest: &Manifest{
		Project: s.project, SchemaVersion: SchemaVersion, CapturedAt: capturedAt.UTC(),
	}}, true)
}

func (s *SnapshotWriter) Asset(asset inventory.CMDBAsset) error {
	if s.count >= MaxAssets {
		return ErrSnapshotLimit
	}
	if err := s.write(record{Type: "asset", Asset: &asset}, true); err != nil {
		return err
	}
	s.count++
	return nil
}

func (s *SnapshotWriter) Finish() error {
	return s.write(record{Type: "complete", Summary: &Summary{
		Count: s.count, SHA256: hex.EncodeToString(s.digest.Sum(nil)),
	}}, false)
}

func (s *SnapshotWriter) write(entry record, hashed bool) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if len(line) > maxLineBytes || s.bytes+len(line) > maxSnapshotBytes {
		return ErrSnapshotLimit
	}
	n, err := s.output.Write(line)
	if err != nil {
		return err
	}
	if n != len(line) {
		return io.ErrShortWrite
	}
	s.bytes += n
	if hashed {
		_, _ = s.digest.Write(line)
	}
	return nil
}
