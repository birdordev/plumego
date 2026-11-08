package timeout

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTimeoutHeaderCommit(t *testing.T) {
	for _, implicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit", true: "implicit"}[implicit], func(t *testing.T) {
			h := Middleware(Config{Timeout: time.Second})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Commit", "before")
				w.Header().Set("X-Delete", "kept")
				w.Header().Set("Trailer", "X-Checksum")
				if implicit {
					_, _ = w.Write([]byte("one"))
				} else {
					w.WriteHeader(201)
				}
				w.Header().Set("X-Commit", "after")
				w.Header().Del("X-Delete")
				w.Header().Set("X-Late", "hidden")
				_, _ = w.Write([]byte("two"))
				w.Header().Set("X-Checksum", "done")
				w.Header().Set(http.TrailerPrefix+"X-Extra", "extra")
			}))
			s := httptest.NewServer(h)
			defer s.Close()
			res, err := http.Get(s.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			_, err = io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if res.Header.Get("X-Commit") != "before" || res.Header.Get("X-Delete") != "kept" || res.Header.Get("X-Late") != "" {
				t.Fatalf("headers=%v", res.Header)
			}
			if res.Trailer.Get("X-Checksum") != "done" || res.Trailer.Get("X-Extra") != "extra" {
				t.Fatalf("trailers=%v", res.Trailer)
			}
		})
	}
}
