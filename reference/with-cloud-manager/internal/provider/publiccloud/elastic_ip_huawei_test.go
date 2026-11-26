package publiccloud

import (
	"strconv"
	"strings"
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestHuaweiElasticIPLargePageContinuation(t *testing.T) {
	page := provider.Page{Next: "61000000-0000-4000-8000-000000000001"}
	for i := range provider.MaxPageObjects + 1 {
		page.Objects = append(page.Objects, provider.Object{ID: strconv.Itoa(i), Status: "DOWN"})
	}
	const marker = "61000000-0000-4000-8000-000000000002"
	first, err := splitHuaweiElasticIPs(page, marker, "", false)
	remote, digest, continues := strings.Cut(first.Next, "~")
	if err != nil || !continues || remote != marker || len(first.Objects) != provider.MaxPageObjects {
		t.Fatalf("invalid first page size=%d cursor=%q error=%v", len(first.Objects), first.Next, err)
	}
	tail, err := splitHuaweiElasticIPs(page, remote, digest, true)
	if err != nil || tail.Next != page.Next || len(tail.Objects) != 1 || tail.Objects[0].ID != strconv.Itoa(provider.MaxPageObjects) {
		t.Fatalf("invalid resumed page: %+v %v", tail, err)
	}
	page.Objects[provider.MaxPageObjects].Status = "ACTIVE"
	if _, err := splitHuaweiElasticIPs(page, remote, digest, true); !provider.IsFailure(err, provider.ExpiredCursor) {
		t.Fatalf("accepted changed Huawei snapshot: %v", err)
	}
}
