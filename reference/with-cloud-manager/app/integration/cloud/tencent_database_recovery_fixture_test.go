package cloudtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

const (
	tencentRecoverySource = "cdb-recoverysource"
	tencentRecoveryTarget = "cdb-recoverytarget"
	tencentRecoveryLost   = "cdb-recoverylost"
	tencentRecoveryJob    = "fixture-async-clone"
	tencentRecoveryTag    = "plumego-cmp-db-restore"
)

const (
	tencentRecoveryNormal int32 = iota
	tencentRecoveryEncrypted
	tencentRecoveryRejected
	tencentRecoveryWrongPoint
	tencentRecoveryDuplicateHistory
	tencentRecoveryLostReceipt
)

type tencentDatabaseRecoveryFixture struct {
	databaseBackupsFixture
	point                             time.Time
	placement                         provider.DatabaseRecoveryPlacement
	mode, writes, dryRuns, asyncReads atomic.Int32
	operation                         atomic.Value
	accepted                          atomic.Pointer[tencentAcceptedClone]
}

type tencentAcceptedClone struct{ marker, id string }

func (f *tencentDatabaseRecoveryFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	action := r.Header.Get("X-TC-Action")
	if action == "GetCallerIdentity" {
		f.serveDatabase(t, w, r)
		return
	}
	if f.serveRestoreWindows(t, w, r) {
		return
	}
	body, err := io.ReadAll(r.Body)
	var request struct {
		InstanceId, AsyncRequestId string
		Limit, Offset              int
		InstanceIds, SubnetIds     []string
		Tags                       []struct{ Key, Value string }
	}
	if err != nil || json.Unmarshal(body, &request) != nil {
		t.Error("invalid native recovery request")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if action == "DescribeDBInstances" && request.Limit == 50 {
		r.Body = io.NopCloser(bytes.NewReader(body))
		f.serveDatabase(t, w, r)
		return
	}
	service, version := "cdb", "2017-03-20"
	switch action {
	case "DescribeSubnets":
		service, version = "vpc", "2017-03-12"
	case "DescribeZones":
		service, version = "cvm", "2017-03-12"
	}
	if r.Method != http.MethodPost || r.Host != service+".tencentcloudapi.com" || r.URL.RequestURI() != "/" ||
		r.Header.Get("X-TC-Version") != version || r.Header.Get("X-TC-Region") != f.region ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		t.Error("unexpected native recovery request scope or authorization")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	reply := func(response map[string]any) { testsupport.WriteJSON(t, w, map[string]any{"Response": response}) }
	mode, clone := f.mode.Load(), f.accepted.Load()
	switch action {
	case "DescribeDBInstances":
		if request.Limit != 2 || request.Offset != 0 {
			t.Error("recovery instance lookup is not bounded")
		}
		rows := []any{}
		if len(request.InstanceIds) == 1 && request.InstanceIds[0] == f.id && len(request.Tags) == 0 {
			rows = append(rows, f.instance(f.id, ""))
		} else if len(request.InstanceIds) == 0 && len(request.Tags) == 1 && clone != nil &&
			request.Tags[0].Key == tencentRecoveryTag && request.Tags[0].Value == clone.marker {
			rows = append(rows, f.instance(clone.id, clone.marker))
		} else {
			t.Error("recovery selected an instance without its exact operation marker")
		}
		reply(map[string]any{"Items": rows, "TotalCount": len(rows)})
	case "DescribeDBInstanceInfo":
		if request.InstanceId != f.id && (clone == nil || request.InstanceId != clone.id) {
			t.Error("encryption check targeted an unrelated instance")
		}
		encryption := "NO"
		if mode == tencentRecoveryEncrypted {
			encryption = "YES"
		}
		reply(map[string]any{"InstanceId": request.InstanceId, "Encryption": encryption, "KeyId": "fixture-private-cdb-key"})
	case "DescribeSubnets":
		if len(request.SubnetIds) != 1 || request.SubnetIds[0] != f.placement.Subnet {
			t.Error("recovery did not check the approved subnet")
		}
		reply(map[string]any{"TotalCount": 1, "SubnetSet": []any{map[string]string{
			"SubnetId": f.placement.Subnet, "VpcId": f.placement.VPC, "Zone": f.placement.Zone,
		}}})
	case "DescribeZones":
		reply(map[string]any{"TotalCount": 1, "ZoneSet": []any{map[string]string{"Zone": f.placement.Zone, "ZoneId": "100003"}}})
	case "CreateCloneInstance":
		f.create(t, w, body, mode, reply)
	case "DescribeAsyncRequestInfo":
		if request.AsyncRequestId != tencentRecoveryJob || clone == nil || clone.id == tencentRecoveryLost {
			t.Error("unexpected native async receipt lookup")
		}
		status := "SUCCESS"
		if f.asyncReads.Add(1) == 1 {
			status = "RUNNING"
		}
		reply(map[string]any{"Status": status, "Info": fixtureDatabaseHost})
	case "DescribeCloneList":
		if request.InstanceId != f.id || request.Limit != 100 || clone == nil || (request.Offset != 0 && request.Offset != 100) {
			t.Error("history was not scoped to the source database")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reply(f.history(mode, clone.id, request.Offset))
	case "DescribeDBSecurityGroups":
		if clone == nil || request.InstanceId != clone.id {
			t.Error("security group check targeted an unrelated instance")
		}
		group := f.placement.SecurityGroup
		if mode == tencentRecoveryLostReceipt {
			group = "sg-unapproved"
		}
		reply(map[string]any{"Groups": []any{map[string]string{"SecurityGroupId": group}}})
	default:
		t.Errorf("unexpected CDB recovery action %q", action)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *tencentDatabaseRecoveryFixture) create(t *testing.T, w http.ResponseWriter, body []byte, mode int32, reply func(map[string]any)) {
	t.Helper()
	var request struct {
		InstanceId, SpecifiedRollbackTime, InstanceName string
		UniqVpcId, UniqSubnetId, Zone, SlaveZone        string
		DeviceType, PayType                             string
		Memory, Cpu, Volume, InstanceNodes              int
		DeployMode                                      *int
		SecurityGroup                                   []string
		ResourceTags                                    []struct {
			TagKey   string
			TagValue []string
		}
		DryRun *bool
	}
	marker := fmt.Sprintf("%x", sha256.Sum256([]byte(tencentRecoveryTag+"\x00"+f.vendor+"\x00"+f.remoteIdentity()+"\x00"+f.region+"\x00"+f.operation.Load().(string))))
	if err := json.Unmarshal(body, &request); err != nil || request.InstanceId != f.id || request.SpecifiedRollbackTime != f.stamp(f.point) ||
		request.InstanceName != recoveryName || request.UniqVpcId != f.placement.VPC || request.UniqSubnetId != f.placement.Subnet ||
		request.Zone != f.placement.Zone || request.SlaveZone != f.placement.Zone || request.PayType != "USED_PAID" ||
		request.DeviceType != f.placement.Flavor || request.Cpu != f.placement.CPU || request.Memory != f.placement.MemoryMiB ||
		request.Volume != f.placement.StorageGiB || request.InstanceNodes != 2 || request.DeployMode == nil || *request.DeployMode != 0 ||
		len(request.SecurityGroup) != 1 || request.SecurityGroup[0] != f.placement.SecurityGroup ||
		len(request.ResourceTags) != 1 || request.ResourceTags[0].TagKey != tencentRecoveryTag ||
		len(request.ResourceTags[0].TagValue) != 1 || request.ResourceTags[0].TagValue[0] != marker || request.DryRun == nil {
		t.Error("native clone request did not preserve the approved intent")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if *request.DryRun {
		f.dryRuns.Add(1)
		if mode == tencentRecoveryRejected {
			reply(map[string]any{"Error": map[string]string{"Code": "InvalidParameterValue", "Message": fixtureDatabaseHost}})
			return
		}
		reply(map[string]any{"RequestId": "fixture-dry-run"})
		return
	}
	f.writes.Add(1)
	if mode == tencentRecoveryLostReceipt {
		f.accepted.Store(&tencentAcceptedClone{marker, tencentRecoveryLost})
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
		return
	}
	f.accepted.Store(&tencentAcceptedClone{marker, tencentRecoveryTarget})
	reply(map[string]any{"AsyncRequestId": tencentRecoveryJob, "RequestId": "fixture-dispatch-request"})
}

func (f *tencentDatabaseRecoveryFixture) stamp(at time.Time) string {
	return at.UTC().Add(8 * time.Hour).Format(time.DateTime)
}

func (f *tencentDatabaseRecoveryFixture) instance(id, marker string) map[string]any {
	row := f.databaseRow(f.product, id, "source-db", "8.0", f.region)
	row["TaskStatus"], row["InstanceType"], row["WanStatus"], row["DiskType"] = 0, 1, 0, ""
	row["DeviceType"], row["Cpu"], row["Memory"], row["Volume"] = f.placement.Flavor, 2, 4096, 20
	if id != f.id {
		row["InstanceName"], row["UniqVpcId"], row["UniqSubnetId"] = recoveryName, f.placement.VPC, f.placement.Subnet
		row["PayType"], row["DeployMode"], row["InstanceNodes"], row["Zone"] = 1, 0, 2, "100003"
		row["Cpu"], row["Memory"], row["Volume"] = f.placement.CPU, f.placement.MemoryMiB, f.placement.StorageGiB
		row["TagList"] = []any{map[string]string{"TagKey": tencentRecoveryTag, "TagValue": marker}}
	}
	return row
}

func (f *tencentDatabaseRecoveryFixture) history(mode int32, target string, offset int) map[string]any {
	items := make([]map[string]any, 0, 100)
	for index := offset; index < min(offset+100, 101); index++ {
		row := map[string]any{"CloneJobId": index + 1, "SrcInstanceId": f.id, "DstInstanceId": "cdb-old" + strconv.Itoa(index),
			"RollbackStrategy": "timepoint", "RollbackTargetTime": f.stamp(f.point), "TaskStatus": "success"}
		if index == 100 || (index == 0 && mode == tencentRecoveryDuplicateHistory) {
			row["DstInstanceId"] = target
			if mode == tencentRecoveryWrongPoint {
				row["RollbackTargetTime"] = f.stamp(f.point.Add(time.Second))
			}
		}
		items = append(items, row)
	}
	return map[string]any{"Items": items, "TotalCount": 101}
}
