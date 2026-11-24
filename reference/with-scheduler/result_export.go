package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/messaging/mq"
)

func (h api) requireTaskRuntime(w http.ResponseWriter, req *http.Request, topic string) bool {
	if topic == sandboxTaskType && !h.sandboxEnabled {
		writeError(w, req, contract.TypeUnavailable, "isolated task runtime unavailable")
		return false
	}
	if topic == exportTaskType && h.objects == nil {
		writeError(w, req, contract.TypeUnavailable, "run result storage unavailable")
		return false
	}
	return true
}

func exportSize(payload json.RawMessage) (int, error) {
	var input struct {
		Size int `json:"size_bytes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Size < 1 || input.Size > maxResultBytes {
		return 0, errInvalidTask
	}
	return input.Size, nil
}

func (r repository) exportHandler(objects *resultObjects) mq.TaskHandler {
	return func(ctx context.Context, task mq.Task) error {
		size, err := exportSize(task.Payload)
		if err != nil {
			return &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
		}
		progress := &progressReporter{write: func(ctx context.Context, percent int) error {
			return r.storeProgress(ctx, task, percent)
		}}
		if err := progress.report(ctx, time.Now(), 0); err != nil {
			return err
		}
		return r.publishResultObject(ctx, objects, task, size, &exportStream{ctx: ctx, size: size, progress: progress})
	}
}

type exportStream struct {
	ctx        context.Context
	size, sent int
	progress   *progressReporter
}

func (s *exportStream) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if s.sent == s.size {
		return 0, io.EOF
	}
	n := min(len(p), s.size-s.sent)
	clear(p[:n])
	s.sent += n
	err := s.progress.report(s.ctx, time.Now(), min(99, s.sent*100/s.size))
	return n, err
}
