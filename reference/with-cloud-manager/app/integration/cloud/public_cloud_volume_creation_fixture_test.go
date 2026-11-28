package cloudtest

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

const volumeCreationMarker = "plumego-cmp-volume"
const volumeCreationName = "fixture-new-volume"
const volumeCreationKMS = "20000000-0000-4000-8000-000000000020"

type volumeCreationFixture struct {
	vpcCreationFixture
	zone, class, firstID, secondID string
	loseReply, wrongCapacity       atomic.Bool
	sends                          atomic.Int32
	mu                             sync.Mutex
	marker, id                     string
}

func (f *volumeCreationFixture) serveVolume(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
		f.serve(t, w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var valid bool
	switch f.vendor {
	case provider.TencentCVM:
		valid = f.serveTencentVolume(t, w, r)
	case provider.AlibabaECS:
		valid = f.serveAlibabaVolume(t, w, r)
	default:
		valid = f.serveHuaweiVolume(t, w, r)
	}
	if !valid {
		t.Errorf("invalid disk provisioning request %s %s %s", r.Method, r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *volumeCreationFixture) rememberVolume(marker string) bool {
	if len(marker) != 64 || marker == f.marker {
		return false
	}
	f.marker, f.id = marker, f.firstID
	if f.sends.Add(1) > 1 {
		f.id = f.secondID
	}
	if f.loseReply.Load() {
		panic(http.ErrAbortHandler)
	}
	return true
}

func (f *volumeCreationFixture) volumeSize() int {
	if f.wrongCapacity.Load() {
		return 41
	}
	return 40
}

func (f *volumeCreationFixture) serveTencentVolume(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Host != "cbs.tencentcloudapi.com" || r.Method != http.MethodPost || r.URL.RequestURI() != "/" ||
		r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Version") != "2017-03-12" ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	switch r.Header.Get("X-TC-Action") {
	case "CreateDisks":
		var input struct {
			DiskName, DiskType, DiskChargeType, ClientToken, Encrypt string
			DiskSize, DiskCount                                      int
			SnapshotId                                               *string
			Placement                                                struct{ Zone string }
			Tags                                                     []struct{ Key, Value string }
		}
		if json.Unmarshal(body, &input) != nil || input.SnapshotId != nil || input.DiskName != volumeCreationName ||
			input.DiskType != f.class || input.Placement.Zone != f.zone || input.DiskSize != 40 || input.DiskCount != 1 ||
			input.DiskChargeType != "POSTPAID_BY_HOUR" || input.Encrypt != "ENCRYPT" || input.ClientToken == "" ||
			len(input.Tags) != 1 || input.Tags[0].Key != volumeCreationMarker || !f.rememberVolume(input.Tags[0].Value) {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"RequestId": "fixture-create", "DiskIdSet": []string{f.id}}})
	case "DescribeDisks":
		var input struct {
			DiskIds []string
			Filters []struct {
				Name   string
				Values []string
			}
		}
		if json.Unmarshal(body, &input) != nil || f.id == "" {
			return false
		}
		exact := len(input.DiskIds) == 1 && input.DiskIds[0] == f.id
		marked := len(input.Filters) == 1 && input.Filters[0].Name == "tag:"+volumeCreationMarker &&
			len(input.Filters[0].Values) == 1 && input.Filters[0].Values[0] == f.marker
		if exact == marked {
			return false
		}
		row := map[string]any{"DiskId": f.id, "DiskName": volumeCreationName, "DiskState": "UNATTACHED", "DiskType": f.class,
			"DiskSize": f.volumeSize(), "Placement": map[string]string{"Zone": f.zone}, "DiskUsage": "DATA_DISK", "DiskChargeType": "POSTPAID_BY_HOUR",
			"Encrypt": true, "Portable": true, "Shareable": false, "Attached": false,
			"Tags": []map[string]string{{"Key": volumeCreationMarker, "Value": f.marker}}}
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"TotalCount": 1, "DiskSet": []any{row}}})
	default:
		return false
	}
	return true
}

func (f *volumeCreationFixture) serveAlibabaVolume(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Method != http.MethodPost || r.URL.Path != "/" ||
		r.Header.Get("X-ACS-Version") != "2014-05-26" || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) || q.Get("RegionId") != f.region ||
		q.Has("SnapshotId") || q.Has("InstanceId") || q.Has("DiskChargeType") {
		return false
	}
	switch r.Header.Get("X-ACS-Action") {
	case "CreateDisk":
		if q.Get("ZoneId") != f.zone || q.Get("DiskCategory") != f.class || q.Get("Size") != "40" || q.Get("DiskName") != volumeCreationName ||
			q.Get("ClientToken") == "" || q.Get("Encrypted") != "true" ||
			q.Get("Tag.1.Key") != volumeCreationMarker || !f.rememberVolume(q.Get("Tag.1.Value")) {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]string{"RequestId": "fixture-create", "DiskId": f.id})
	case "DescribeDisks":
		if f.id == "" || (q.Get("DiskIds") != `["`+f.id+`"]` &&
			(q.Get("Tag.1.Key") != volumeCreationMarker || q.Get("Tag.1.Value") != f.marker)) {
			return false
		}
		row := map[string]any{"DiskId": f.id, "DiskName": volumeCreationName, "RegionId": f.region, "ZoneId": f.zone, "Status": "Available",
			"Category": f.class, "DiskChargeType": "PostPaid", "Type": "data", "Size": f.volumeSize(), "Portable": true,
			"MultiAttach": "Disabled", "Encrypted": true,
			"Tags": map[string]any{"Tag": []map[string]string{{"TagKey": volumeCreationMarker, "TagValue": f.marker}}}}
		testsupport.WriteJSON(t, w, map[string]any{"Disks": map[string]any{"Disk": []any{row}}})
	default:
		return false
	}
	return true
}

func (f *volumeCreationFixture) serveHuaweiVolume(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	if r.Host != "evs."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	path := "/v2/" + fixtureHuaweiProject + "/cloudvolumes"
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v2.1"+strings.TrimPrefix(path, "/v2"):
		var input struct {
			Billing json.RawMessage `json:"bssParam"`
			Volume  struct {
				Name, Description string
				SnapshotID        *string `json:"snapshot_id"`
				Zone              string  `json:"availability_zone"`
				Type              string  `json:"volume_type"`
				Size, Count       int
				Multiattach       *bool
				Metadata          map[string]string
			}
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			return false
		}
		v := input.Volume
		marker, ok := strings.CutPrefix(v.Description, volumeCreationMarker+":")
		if !ok || len(input.Billing) != 0 || v.SnapshotID != nil || v.Zone != f.zone || v.Type != f.class ||
			v.Name != volumeCreationName || v.Size != 40 || v.Count != 1 || v.Multiattach == nil || *v.Multiattach ||
			v.Metadata["__system__encrypted"] != "1" || v.Metadata["__system__cmkid"] != volumeCreationKMS ||
			len(r.Header.Get("X-Client-Token")) != 36 || !f.rememberVolume(marker) {
			return false
		}
		w.WriteHeader(http.StatusAccepted)
		testsupport.WriteJSON(t, w, map[string]any{"job_id": "fixture-create", "volume_ids": []string{f.id}})
	case r.Method == http.MethodGet && f.id != "":
		row := map[string]any{"id": f.id, "name": volumeCreationName, "description": volumeCreationMarker + ":" + f.marker,
			"os-vol-tenant-attr:tenant_id": fixtureHuaweiProject, "availability_zone": f.zone, "volume_type": f.class,
			"status": "available", "size": f.volumeSize(), "bootable": "false", "multiattach": false, "attachments": []any{},
			"metadata": map[string]string{"__system__encrypted": "1"}}
		switch r.URL.Path {
		case path + "/" + f.id:
			testsupport.WriteJSON(t, w, map[string]any{"volume": row})
		case path + "/detail":
			q := r.URL.Query()
			if q.Get("name") != volumeCreationName || q.Get("sort_key") != "id" || q.Get("sort_dir") != "asc" {
				return false
			}
			rows := []any{}
			if q.Get("marker") == "" {
				rows = append(rows, row)
			} else if q.Get("marker") != f.id {
				return false
			}
			testsupport.WriteJSON(t, w, map[string]any{"volumes": rows})
		default:
			return false
		}
	default:
		return false
	}
	return true
}
