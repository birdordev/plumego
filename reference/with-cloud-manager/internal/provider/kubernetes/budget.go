package kubernetes

import (
	"errors"
	"io"
	"sync/atomic"
)

type observationBudget struct {
	lists atomic.Int64
	bytes atomic.Int64
}

var errObservationBudget = errors.New("exceeded Kubernetes workload observation byte budget")

func newObservationBudget() *observationBudget {
	budget := new(observationBudget)
	budget.bytes.Store(maxReadBytes)
	return budget
}

func (b *observationBudget) reader(source io.Reader) io.Reader {
	if b == nil {
		return source
	}
	return budgetReader{source, &b.bytes}
}

type budgetReader struct {
	source    io.Reader
	remaining *atomic.Int64
}

func (r budgetReader) Read(p []byte) (int, error) {
	for {
		remaining := r.remaining.Load()
		if remaining == 0 {
			return 0, errObservationBudget
		}
		limit := min(int64(len(p)), remaining)
		if r.remaining.CompareAndSwap(remaining, remaining-limit) {
			n, err := r.source.Read(p[:limit])
			r.remaining.Add(limit - int64(n))
			return n, err
		}
	}
}
