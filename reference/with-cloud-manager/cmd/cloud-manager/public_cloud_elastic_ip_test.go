package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const (
	firstPublicAddress   = "192.0.2.10"
	changedPublicAddress = "192.0.2.11"
	huaweiEIPFirst       = "61000000-0000-4000-8000-000000000001"
	huaweiEIPSecond      = "61000000-0000-4000-8000-000000000002"
	huaweiEIPPort        = "62000000-0000-4000-8000-000000000001"
)

type elasticIPFixture struct {
	publicCloudResourceFixture
	first, continued atomic.Int32
	foreign, changed atomic.Bool
}

func (f *elasticIPFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	firstIP := firstPublicAddress
	if f.changed.Load() {
		firstIP = changedPublicAddress
	}
	count := func(continuation bool) bool {
		if !continuation {
			f.first.Add(1)
			return true
		}
		if f.continued.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return false
		}
		return true
	}
	invalid := func() bool {
		t.Error("unexpected elastic IP catalogue request")
		w.WriteHeader(http.StatusBadRequest)
		return true
	}
	switch {
	case r.Header.Get("X-TC-Action") == "DescribeAddresses":
		body, err := io.ReadAll(r.Body)
		var request struct{ Offset, Limit int }
		if err != nil || !validFixtureTencentSignature(r, body, f.key, f.secret) ||
			r.Host != "vpc.tencentcloudapi.com" || r.Header.Get("X-TC-Version") != "2017-03-12" ||
			r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != f.session ||
			json.Unmarshal(body, &request) != nil || request.Limit != 100 || (request.Offset != 0 && request.Offset != 1) {
			return invalid()
		}
		if !count(request.Offset == 1) {
			return true
		}
		row := map[string]any{"AddressId": "eip-first", "AddressName": "primary-public-ip", "AddressStatus": "BIND", "AddressIp": firstIP,
			"AddressType": "EIP", "InstanceType": "CVM", "InstanceId": "ins-1", "TagSet": []any{map[string]string{"Key": "purpose", "Value": "demo"}}}
		if request.Offset == 1 {
			row = map[string]any{"AddressId": "eip-second", "AddressStatus": "UNBIND", "AddressIp": "192.0.2.20", "AddressType": "AnycastEIP",
				"InstanceType": "CVM", "InstanceId": "ins-1"}
			if f.foreign.Load() {
				row["AddressType"] = "WanIP"
			}
		}
		writeCloudFixtureJSON(t, w, map[string]any{"Response": map[string]any{"TotalCount": 2, "AddressSet": []any{row}}})
	case r.Header.Get("X-ACS-Action") == "DescribeEipAddresses":
		q := r.URL.Query()
		number := q.Get("PageNumber")
		if r.Host != "vpc.aliyuncs.com" || r.Header.Get("X-ACS-Version") != "2016-04-28" || r.Header.Get("X-ACS-Security-Token") != f.session ||
			!validFixtureAlibabaSignature(r, f.key, f.secret) || q.Get("RegionId") != f.region || q.Get("PageSize") != "100" ||
			(number != "1" && number != "2") {
			return invalid()
		}
		if !count(number == "2") {
			return true
		}
		rows := make([]map[string]any, 0, 100)
		if number == "1" {
			rows = append(rows, map[string]any{"AllocationId": "eip-first", "Name": "primary-public-ip", "IpAddress": firstIP,
				"Status": "InUse", "RegionId": f.region, "InstanceType": "EcsInstance", "InstanceId": "i-1", "InstanceRegionId": f.region,
				"Tags": map[string]any{"Tag": []any{map[string]string{"Key": "purpose", "Value": "demo"}}}})
			for i := 1; i < 100; i++ {
				rows = append(rows, map[string]any{"AllocationId": fmt.Sprintf("eip-paging-%d", i), "IpAddress": fmt.Sprintf("198.51.100.%d", i),
					"Status": "Available", "RegionId": f.region})
			}
		} else {
			region := f.region
			if f.foreign.Load() {
				region = "cn-beijing"
			}
			rows = append(rows, map[string]any{"AllocationId": "eip-second", "IpAddress": "192.0.2.20", "Status": "InUse",
				"RegionId": region, "InstanceType": "IpAddress", "VpcId": f.id(provider.Network)})
		}
		writeCloudFixtureJSON(t, w, map[string]any{"PageNumber": json.Number(number), "PageSize": 100, "TotalCount": 101,
			"EipAddresses": map[string]any{"EipAddress": rows}})
	case r.URL.Path == "/v3/"+f.project+"/eip/publicips":
		q := r.URL.Query()
		marker := q.Get("marker")
		if r.Host != "vpc."+f.region+".myhuaweicloud.com" || r.Method != http.MethodGet ||
			r.Header.Get("X-Auth-Token") != f.token || q.Get("limit") != "2000" ||
			!slices.Contains(q["fields"], "project_id") || !slices.Contains(q["fields"], "vnic") ||
			(marker != "" && marker != huaweiEIPFirst) {
			return invalid()
		}
		if !count(marker != "") {
			return true
		}
		row := map[string]any{"id": huaweiEIPFirst, "alias": "primary-public-ip", "status": "ACTIVE", "type": "DUALSTACK",
			"project_id": f.project, "public_ip_address": firstIP, "public_ipv6_address": "2001:db8:0:0::1",
			"tags": []string{"purpose=demo"}, "associate_instance_type": "PORT", "associate_instance_id": huaweiEIPPort,
			"vnic": map[string]string{"port_id": huaweiEIPPort, "device_id": "00000000-0000-4000-8000-000000000001",
				"device_owner": "compute:nova", "vpc_id": f.id(provider.Network)}}
		if f.changed.Load() {
			row["associate_instance_id"] = "00000000-0000-4000-8000-000000000001"
		}
		next := huaweiEIPFirst
		if marker != "" {
			row = map[string]any{"id": huaweiEIPSecond, "status": "DOWN", "type": "DUALSTACK", "project_id": f.project,
				"public_ip_address": "0.0.0.0", "public_ipv6_address": "2001:db8::2"}
			if f.foreign.Load() {
				row["project_id"] = "00000000000000000000000000000000"
			}
			next = ""
		}
		writeCloudFixtureJSON(t, w, map[string]any{"publicips": []any{row}, "page_info": map[string]any{"current_count": 1, "next_marker": next}})
	default:
		return false
	}
	return true
}

func (f *elasticIPFixture) exercise(t *testing.T, db *sql.DB, app http.Handler, token, account, profile string, scan func(context.Context) error) {
	t.Helper()
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_asset_tag_rules(tenant_id,account_id,tag_key,tag_value,project_id)
		VALUES ('tenant',$1,'purpose','demo','project')`, account); err != nil {
		t.Fatal(err)
	}
	if err := scan(ctx); !provider.IsFailure(err, provider.Throttled) {
		t.Fatalf("elastic IP continuation did not surface throttling: %v", err)
	}
	cursor := func() string {
		t.Helper()
		var value string
		err := db.QueryRowContext(ctx, "SELECT cursor FROM cmp_inventory_scopes WHERE account_id=$1 AND kind='elastic-ip'", account).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	resume := cursor()
	if resume == "" {
		t.Fatal("elastic IP checkpoint was lost")
	}
	f.foreign.Store(true)
	failure := provider.Unauthorized
	if f.provider == provider.TencentCVM {
		failure = provider.InvalidResponse
	}
	if err := scan(ctx); !provider.IsFailure(err, failure) || cursor() != resume {
		t.Fatalf("foreign address page changed the checkpoint: %v", err)
	}
	f.foreign.Store(false)
	if err := scan(ctx); err != nil || f.first.Load() != 1 || f.continued.Load() != 3 || cursor() != "" {
		t.Fatalf("elastic IP pagination first=%d continuation=%d error=%v", f.first.Load(), f.continued.Load(), err)
	}
	get := func(project, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant/projects/"+project+"/assets"+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("elastic IP API response %d %s", w.Code, w.Body)
		}
		return w
	}
	filter := "?kind=elastic-ip&account=" + url.QueryEscape(account) + "&q=" + firstPublicAddress
	var topology inventory.Topology
	decodeProjectData(t, get("project", "/topology"+filter), &topology)
	want := []string{firstPublicAddress}
	instance := "ins-1"
	switch f.provider {
	case provider.AlibabaECS:
		instance = "i-1"
	case provider.HuaweiECS:
		instance = "00000000-0000-4000-8000-000000000001"
		want = append(want, "2001:db8::1")
		var ipv6 inventory.Assets
		decodeProjectData(t, get("project", "?kind=elastic-ip&account="+url.QueryEscape(account)+"&q=2001:db8::2"), &ipv6)
		if len(ipv6.Items) != 1 || !slices.Equal(ipv6.Items[0].PublicAddresses, []string{"2001:db8::2"}) {
			t.Fatalf("Huawei IPv6 inventory included a placeholder: %+v", ipv6.Items)
		}
	}
	if len(topology.Nodes) != 1 || !slices.Equal(topology.Nodes[0].Asset.PublicAddresses, want) ||
		!slices.ContainsFunc(topology.Nodes[0].Links, func(link inventory.TopologyLink) bool {
			return link.Relation == "bound_to" && link.ID == instance && link.Kind == provider.Instance
		}) {
		t.Fatalf("elastic IP topology mismatch: %+v", topology.Nodes)
	}
	records, err := csv.NewReader(get("project", "/export"+filter).Body).ReadAll()
	if err != nil || len(records) != 2 || records[0][len(records[0])-1] != "public_addresses" ||
		records[1][len(records[1])-1] != strings.Join(want, " ") {
		t.Fatalf("elastic IP CSV mismatch: %v %v", records, err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_projects VALUES ('tenant','other') ON CONFLICT DO NOTHING;
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role)
		VALUES ('tenant','other','admin','viewer') ON CONFLICT DO NOTHING;`)
	exec("INSERT INTO cmp_cloud_account_grants(tenant_id,account_id,project_id) VALUES ('tenant',$1,'other')", account)
	exec(`INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,interval_seconds,enabled)
		VALUES ('tenant',$1,$2,'security-group-rule',300,false)`, account, f.region)
	var assets inventory.Assets
	decodeProjectData(t, get("other", filter), &assets)
	if len(assets.Items) != 0 {
		t.Fatalf("shared account exposed elastic IPs mapped to another project: %+v", assets)
	}
	f.changed.Store(true)
	runFixtureScheduledScan(t, db, inventory.Key{Tenant: "tenant", Account: account, Region: f.region, Kind: provider.ElasticIP}, f.provider, profile, scan)
	var changes inventory.Changes
	decodeProjectData(t, get("project", "/changes"+filter), &changes)
	after := slices.Clone(want)
	after[0] = changedPublicAddress
	if !slices.ContainsFunc(changes.Items, func(change inventory.Change) bool {
		return change.Action == "changed" && change.PreviousName != nil && change.Name == *change.PreviousName &&
			slices.Equal(change.PreviousPublicAddresses, want) && slices.Equal(change.PublicAddresses, after)
	}) {
		t.Fatalf("missing address-only change in history: %+v", changes.Items)
	}
	topology = inventory.Topology{}
	decodeProjectData(t, get("project", "/topology"+strings.Replace(filter, firstPublicAddress, changedPublicAddress, 1)), &topology)
	if len(topology.Nodes) != 1 || !slices.Equal(topology.Nodes[0].Asset.PublicAddresses, after) ||
		!slices.ContainsFunc(topology.Nodes[0].Links, func(link inventory.TopologyLink) bool {
			return link.Relation == "bound_to" && link.ID == instance && link.Kind == provider.Instance
		}) {
		t.Fatalf("elastic IP changes lost bound instance: %+v", topology.Nodes)
	}
}

func runFixtureScheduledScan(t *testing.T, db *sql.DB, expected inventory.Key, driverName, profile string, scan func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	var id int64
	err := db.QueryRowContext(ctx, `INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,interval_seconds)
		VALUES ($1,$2,$3,$4,300) RETURNING schedule_id`, expected.Tenant, expected.Account, expected.Region, expected.Kind).Scan(&id)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- controller.NewStore(db).Run(ctx, func(ctx context.Context, key inventory.Key, driver, reference string) error {
			if key != expected || driver != driverName || reference != profile {
				return fmt.Errorf("unexpected scheduled scope: %+v", key)
			}
			return scan(ctx)
		})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var outcome sql.NullString
		err := db.QueryRowContext(ctx, "SELECT last_outcome FROM cmp_inventory_schedules WHERE schedule_id=$1", id).Scan(&outcome)
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Valid {
			if outcome.String != "success" {
				t.Fatalf("%s schedule outcome %s", expected.Kind, outcome.String)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s schedule did not finish", expected.Kind)
		case <-ticker.C:
		}
	}
}
