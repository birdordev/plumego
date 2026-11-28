package cloudtest

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func (f publicCloudResourceFixture) serveVolumes(t *testing.T, w http.ResponseWriter, r *http.Request, size int64) bool {
	t.Helper()
	var result any
	fail := func() bool {
		t.Errorf("incorrect storage inventory request %s %s", r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		return true
	}
	id := f.id(provider.Volume)
	switch {
	case r.Host == "cbs.tencentcloudapi.com":
		body, err := io.ReadAll(r.Body)
		if err != nil || !validFixtureTencentSignature(r, body, f.key, f.secret) ||
			r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != f.session ||
			r.Header.Get("X-TC-Action") != "DescribeDisks" || r.Header.Get("X-TC-Version") != "2017-03-12" {
			return fail()
		}
		var page struct{ Offset, Limit *int }
		if json.Unmarshal(body, &page) != nil || page.Offset == nil || page.Limit == nil || *page.Limit != 100 ||
			(*page.Offset != 0 && *page.Offset != 1) {
			return fail()
		}
		row := map[string]any{"DiskId": id, "DiskName": "fixture-volume", "DiskState": "ATTACHED",
			"DiskSize": size, "DiskType": "CLOUD_SSD", "DiskUsage": "DATA_DISK",
			"InstanceId": "ins-1", "InstanceIdList": []string{"ins-1", "ins-2"}, "Placement": map[string]string{"Zone": f.region + "-1"}}
		if *page.Offset == 1 {
			row = map[string]any{"DiskId": "disk-2", "DiskState": "UNATTACHED"}
		}
		result = map[string]any{"Response": map[string]any{"TotalCount": 2, "DiskSet": []any{row}}}
	case r.Host == "ecs.cn-hangzhou.aliyuncs.com" && r.Header.Get("X-ACS-Action") == "DescribeDisks":
		const next = "disk+next/page=2"
		q := r.URL.Query()
		cursor := q.Get("NextToken")
		if (cursor != "" && cursor != next) || q.Get("MaxResults") != "100" || q.Get("RegionId") != f.region ||
			!validFixtureAlibabaSignature(r, f.key, f.secret) || r.Header.Get("X-ACS-Version") != "2014-05-26" {
			return fail()
		}
		token := next
		row := map[string]any{"DiskId": id, "DiskName": "fixture-volume", "Status": "In_use", "RegionId": f.region, "ZoneId": f.id(provider.Zone),
			"Size": size, "Category": "cloud_essd", "Type": "system",
			"InstanceId": "i-1", "MountInstances": map[string]any{"MountInstance": []any{
				map[string]string{"InstanceId": "i-1"}, map[string]string{"InstanceId": "i-2"},
			}},
		}
		if cursor != "" {
			token = ""
			row = map[string]any{"DiskId": "d-2", "Status": "Available", "RegionId": f.region}
		}
		result = map[string]any{"NextToken": token, "Disks": map[string]any{"Disk": []any{row}}}
	case r.Host == "evs.cn-north-4.myhuaweicloud.com":
		path := "/v2/" + f.project
		q := r.URL.Query()
		marker := q.Get("marker")
		if r.Method != http.MethodGet || r.Header.Get("X-Auth-Token") != f.token ||
			r.URL.Path != path+"/cloudvolumes/detail" || q.Get("limit") != "100" || (marker != "" && marker != id) {
			return fail()
		}
		row := map[string]any{"id": id, "name": "fixture-volume", "status": "in-use", "os-vol-tenant-attr:tenant_id": f.project,
			"size": size, "volume_type": "GPSSD", "bootable": "false",
			"availability_zone": f.id(provider.Zone),
			"attachments": []any{
				map[string]string{"server_id": "00000000-0000-4000-8000-000000000001"},
				map[string]string{"server_id": "00000000-0000-4000-8000-000000000002"},
			},
		}
		links := []any{}
		if marker == "" {
			links = append(links, map[string]string{"rel": "next", "href": "https://volume.internal.example:8776" + path + "/volumes/detail?limit=100&marker=" + id})
		} else {
			row = map[string]any{"id": "40000000-0000-4000-8000-000000000002", "status": "available", "os-vol-tenant-attr:tenant_id": f.project}
		}
		result = map[string]any{"volumes": []any{row}, "volumes_links": links}
	default:
		return false
	}
	if err := json.NewEncoder(w).Encode(result); err != nil {
		t.Error(err)
	}
	return true
}

func (f publicCloudResourceFixture) checkVolumeInventory(t *testing.T, app http.Handler, base, account, token string, size *atomic.Int64, scan func() error) {
	t.Helper()
	class, usage, bootable := "CLOUD_SSD", "data", ""
	switch f.provider {
	case provider.AlibabaECS:
		class, usage = "cloud_essd", "system"
	case provider.HuaweiECS:
		class, usage, bootable = "GPSSD", "", "false"
	}
	query := "?kind=volume&account=" + account
	volume := func(want int) {
		t.Helper()
		var assets inventory.Assets
		testsupport.DecodeProjectData(t, freshnessRequest(t, app, base+"/assets"+query+"&q="+url.QueryEscape(class), token), &assets)
		if len(assets.Items) != 1 || assets.Items[0].ID != f.id(provider.Volume) {
			t.Fatalf("class search did not return the expected volume: %+v", assets)
		}
		found := assets.Items[0].Volume
		if found == nil || found.SizeGiB == nil || *found.SizeGiB != want || found.StorageClass != class || found.Usage != usage ||
			(found.Bootable != nil) != (bootable != "") || (found.Bootable != nil && *found.Bootable) {
			t.Fatalf("unexpected volume attributes %+v, want %d GiB, %s, %s, %s", found, want, class, usage, bootable)
		}
	}
	volume(60)
	size.Store(120)
	if err := scan(); err != nil {
		t.Fatal(err)
	}
	volume(120)
	size.Store(-1)
	if err := scan(); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("negative size was accepted: %v", err)
	}
	volume(120)
	response := freshnessRequest(t, app, base+"/assets/export"+query, token)
	rows, err := csv.NewReader(response.Body).ReadAll()
	if err != nil || response.Code != http.StatusOK || len(rows) != 3 || len(rows[0]) != 13 ||
		!slices.Equal(rows[0][9:], []string{"size_gib", "storage_class", "usage", "bootable"}) {
		t.Fatalf("invalid volume CSV: %v, %v", rows, err)
	}
	for _, row := range rows[1:] {
		if row[3] == f.id(provider.Volume) {
			if !slices.Equal(row[9:], []string{"120", class, usage, bootable}) {
				t.Fatalf("invalid populated CSV row: %v", row)
			}
		} else if !slices.Equal(row[9:], []string{"", "", "", ""}) {
			t.Fatalf("unknown volume attributes were synthesized: %v", row)
		}
	}
	var history inventory.Changes
	testsupport.DecodeProjectData(t, freshnessRequest(t, app, base+"/assets/changes"+query+"&q="+url.QueryEscape(class), token), &history)
	if len(history.Items) != 2 || history.Items[0].Action != "changed" || history.Items[0].PreviousVolume == nil || history.Items[0].Volume == nil ||
		history.Items[0].PreviousVolume.SizeGiB == nil || *history.Items[0].PreviousVolume.SizeGiB != 60 ||
		history.Items[0].Volume.SizeGiB == nil || *history.Items[0].Volume.SizeGiB != 120 {
		t.Fatalf("volume change history did not retain both observations: %+v", history)
	}
	for _, endpoint := range []string{"/assets", "/assets/changes", "/assets/export"} {
		outside := strings.Replace(base, "/tenants/tenant/", "/tenants/foreign/", 1) + endpoint + query
		requireFreshnessStatus(t, freshnessRequest(t, app, outside, token), http.StatusForbidden)
	}
}
