package transport_test

import (
	"fmt"
	"net/http/httptest"
	"time"

	"github.com/birdordev/plumego/x/tenant/transport"
)

func Example() {
	rec := httptest.NewRecorder()

	transport.SetRetryAfterHeader(rec, 45*time.Second)
	transport.SetQuotaHeaders(rec, 9, 120)

	fmt.Println(transport.HeaderOrDefault("", transport.DefaultTenantHeader))
	fmt.Println(rec.Header().Get("Retry-After"))
	fmt.Println(rec.Header().Get("X-Quota-Remaining-Requests"))
	fmt.Println(rec.Header().Get("X-Quota-Remaining-Tokens"))

	// Output:
	// X-Tenant-ID
	// 45
	// 9
	// 120
}
