package httpmetrics_test

import (
	"context"
	"github.com/spcent/plumego/middleware/httpmetrics"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type discardResponse struct{ header http.Header }

func (d discardResponse) Header() http.Header         { return d.header }
func (d discardResponse) WriteHeader(int)             {}
func (d discardResponse) Write(p []byte) (int, error) { return len(p), nil }

type byteObserver struct{ counts chan int }

func (o byteObserver) ObserveHTTP(_ context.Context, _ string, _ string, _ int, bytes int, _ time.Duration) {
	o.counts <- bytes
}

func TestObservationDoesNotCaptureBody(t *testing.T) {
	counts := make(chan int, 2)
	h := httpmetrics.Middleware(byteObserver{counts})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1<<20)
		for i := 0; i < 16; i++ {
			if _, err := w.Write(chunk); err != nil {
				t.Fatal(err)
			}
		}
		if captured, ok := w.(interface{ Body() []byte }); ok && len(captured.Body()) != 0 {
			t.Fatalf("observation retained %d response bytes", len(captured.Body()))
		}
	}))
	h = httpmetrics.Middleware(byteObserver{counts})(h)
	h.ServeHTTP(discardResponse{make(http.Header)}, httptest.NewRequest("GET", "/", nil))
	for i := 0; i < 2; i++ {
		if got := <-counts; got != 16<<20 {
			t.Fatalf("observed bytes=%d", got)
		}
	}
}
