package contract_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
)

func TestDecodeJSONRequest(t *testing.T) {
	for _, tc := range []struct {
		name, media, body string
		limit             int64
		unknownLength     bool
		status            int
	}{
		{"one document", "application/json; charset=utf-8", "{\"name\":\"test\"} \n", 0, false, 200},
		{"vendor type", "application/vnd.plumego+json", "{\"name\":\"test\"}", 0, false, 200},
		{"wrong media", "text/plain", "{}", 0, false, 415},
		{"two documents", "application/json", "{} {}", 0, false, 400},
		{"malformed suffix", "application/json", "{} %", 0, false, 400},
		{"unknown field", "application/json", "{\"secret\":true}", 0, false, 400},
		{"empty", "application/json", "", 0, false, 400},
		{"null", "application/json", "null", 0, false, 400},
		{"exact limit", "application/json", "{} ", 3, true, 200},
		{"known length over limit", "application/json", "{} ", 2, false, 413},
		{"unknown length over limit", "application/json", "{} ", 2, true, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.media)
			if tc.unknownLength {
				r.ContentLength = -1
			}
			var dst struct{ Name string }
			err := contract.DecodeJSONRequest(r, &dst, contract.JSONRequestOptions{MaxBytes: tc.limit})
			if tc.status == http.StatusOK {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			apiErr, ok := contract.AsAPIError(err)
			if !ok || apiErr.Status() != tc.status {
				t.Fatalf("error = %v, want API status %d", err, tc.status)
			}
		})
	}
}

func TestRejectDuplicateJSONFields(t *testing.T) {
	for _, tc := range []struct {
		body    string
		invalid bool
	}{
		{`{"a":[{"x":1},{"x":2}],"b":{}}`, false},
		{`{"a":{"x":1,"\u0078":2}}`, true},
		{`[{"a":1,"a":2}]`, true},
	} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		var value any
		err := contract.DecodeUniqueJSONRequest(r, &value, contract.JSONRequestOptions{})
		if (err != nil) != tc.invalid {
			t.Errorf("%s: %v", tc.body, err)
		}
	}
}
