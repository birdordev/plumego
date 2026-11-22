package kvstore

import "sync"

type mutationChange struct {
	rollback func()
	publish  func()
}

type mutationRequest struct {
	done   <-chan struct{}
	stage  func() (mutationChange, error)
	result chan error
}

type mutationQueue struct {
	slots   chan struct{}
	mu      sync.Mutex
	pending []mutationRequest
	running bool
}

func (kv *KVStore) submitMutation(done <-chan struct{}, stage func() (mutationChange, error)) error {
	queue := &kv.mutations
	if queue.slots == nil {
		return ErrStoreClosed
	}
	select {
	case <-done:
		return errMutationCanceled
	case queue.slots <- struct{}{}:
	}
	defer func() { <-queue.slots }()
	req := mutationRequest{done: done, stage: stage, result: make(chan error, 1)}
	queue.mu.Lock()
	queue.pending = append(queue.pending, req)
	if !queue.running {
		queue.running = true
		go kv.drainMutations()
	}
	queue.mu.Unlock()
	return <-req.result
}

func (kv *KVStore) drainMutations() {
	queue := &kv.mutations
	for {
		queue.mu.Lock()
		if len(queue.pending) == 0 {
			queue.running = false
			queue.mu.Unlock()
			return
		}
		count := min(len(queue.pending), 32)
		var batch [32]mutationRequest
		copy(batch[:], queue.pending[:count])
		copy(queue.pending, queue.pending[count:])
		clear(queue.pending[len(queue.pending)-count:])
		queue.pending = queue.pending[:len(queue.pending)-count]
		queue.mu.Unlock()
		kv.commitMutations(batch[:count])
	}
}

func (kv *KVStore) commitMutations(batch []mutationRequest) {
	kv.writeMu.Lock()
	defer kv.writeMu.Unlock()
	kv.mu.Lock()
	defer kv.mu.Unlock()

	var changes [32]mutationChange
	var outcomes [32]error
	for {
		clear(changes[:])
		var staged int
		for i, req := range batch {
			switch {
			case cancellationSignaled(req.done):
				outcomes[i] = errMutationCanceled
			case kv.closed || kv.data == nil:
				outcomes[i] = ErrStoreClosed
			default:
				changes[i], outcomes[i] = req.stage()
				if outcomes[i] == nil {
					staged++
				}
			}
		}

		// Once persistence starts, all staged operations share the result
		// and the same rename. Readers observe the committed state only.
		var commitErr error
		if staged != 0 {
			commitErr = kv.persistMutationLocked(func() bool {
				for i, req := range batch {
					if changes[i].rollback != nil && cancellationSignaled(req.done) {
						return true
					}
				}
				return false
			}, func() {
				for i := len(batch) - 1; i >= 0; i-- {
					if changes[i].rollback != nil {
						changes[i].rollback()
					}
				}
			}, func() {
				for _, change := range changes[:len(batch)] {
					if change.publish != nil {
						change.publish()
					}
				}
			})
		}
		if commitErr == errMutationCanceled {
			continue
		}
		for i, req := range batch {
			err := outcomes[i]
			if err == nil || (err == ErrKeyNotFound && commitErr != nil) {
				err = commitErr
			}
			req.result <- err
		}
		return
	}
}
