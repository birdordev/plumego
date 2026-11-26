package publiccloud

import (
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestBucketPageCompleteness(t *testing.T) {
	for _, test := range []struct {
		product  string
		count    int
		current  string
		response bucketList
		want     string
		valid    bool
	}{
		{provider.HuaweiOBS, 100, "", bucketList{}, "", true},
		{provider.AlibabaOSS, 99, "", bucketList{}, "", true},
		{provider.AlibabaOSS, 100, "", bucketList{}, "", false},
		{provider.TencentCOS, 10, "", bucketList{}, "", false},
		{provider.TencentCOS, 10, "", bucketList{Truncated: []string{"false"}}, "", true},
		{provider.TencentCOS, 10, "", bucketList{Truncated: []string{"true"}, NextMarkers: []string{"valid-next"}}, "valid-next", true},
		{provider.TencentCOS, 10, "same", bucketList{Truncated: []string{"true"}, NextMarkers: []string{"same"}}, "", false},
		{provider.TencentCOS, 10, "", bucketList{Truncated: []string{"true", "false"}}, "", false},
		{provider.HuaweiOBS, 10, "", bucketList{Truncated: []string{"true"}, NextMarkers: []string{"unsupported"}}, "", false},
	} {
		cursor, err := test.response.nextCursor(test.product, test.current, test.count, 100)
		if (err == nil) != test.valid || cursor != test.want {
			t.Fatalf("product=%s, response=%+v: got %q %v", test.product, test.response, cursor, err)
		}
	}
}

func TestBucketIdentityUsesCreationInstant(t *testing.T) {
	row := bucketRow{Name: "fixture-bucket", Location: "oss-cn-shanghai", Region: "cn-shanghai", CreationDate: "2025-01-01T18:00:00+08:00"}
	first, err := row.asset(provider.AlibabaOSS)
	if err != nil || first.ID != "fixture-bucket@2025-01-01T10:00:00Z" {
		t.Fatalf("unexpected bucket identity: %s %v", first.ID, err)
	}
	row.CreationDate = "2025-01-01T10:00:00.010Z"
	replacement, err := row.asset(provider.AlibabaOSS)
	if err != nil || first.ID == replacement.ID {
		t.Fatalf("bucket reuse of the same name kept the old identity: %s %v", replacement.ID, err)
	}
	row.Region = "cn-beijing"
	if _, err := row.asset(provider.AlibabaOSS); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("conflicting OSS locations survived projection: %v", err)
	}
}
