package cloudtest

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

const (
	alibabaRecoverySource       = "rm-recoverysource"
	alibabaRecoveryTarget       = "rm-recoverytarget"
	alibabaRecoveryVPC          = "vpc-isolatedrds"
	alibabaRecoverySubnet       = "vsw-isolatedrds"
	alibabaRecoveryJob          = "1E43AAE0-BEE8-43DA-860D-EAF2AA0724DC"
	alibabaRecoveryKey          = "749c1df7-1c08-4239-85f6-7db1213557ae"
	alibabaRecoveryPublicSource = 1
	alibabaRecoveryPublicTarget = 2
	alibabaRecoveryWrongKey     = 3
	alibabaRecoveryLostReceipt  = 4
	alibabaRecoveryOpenList     = 5
	alibabaRecoverySecurityLink = 6
)

type alibabaDatabaseRecoveryFixture struct {
	databaseFixture
	point        time.Time
	placement    provider.DatabaseRecoveryPlacement
	mode, writes atomic.Int32
	token        atomic.Value
}

func (f *alibabaDatabaseRecoveryFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	action := r.Header.Get("X-ACS-Action")
	switch action {
	case "GetCallerIdentity", "DescribeDBInstances":
		f.serveDatabase(t, w, r)
		return
	}
	if r.Method != http.MethodPost || r.Host != "rds.aliyuncs.com" || r.Header.Get("X-ACS-Version") != "2014-08-15" ||
		r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession || !validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		t.Error("unexpected recovery request signature or scope")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	q := r.URL.Query()
	id, mode := q.Get("DBInstanceId"), f.mode.Load()
	if id != alibabaRecoverySource && id != alibabaRecoveryTarget {
		t.Error("recovery request targeted an unrelated instance")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch action {
	case "CloneDBInstance":
		if id != alibabaRecoverySource || q.Get("ClientToken") != f.token.Load() ||
			q.Get("RegionId") != f.region || q.Get("RestoreTime") != f.point.UTC().Format(time.RFC3339) ||
			q.Get("DBInstanceDescription") != recoveryName || q.Get("InstanceNetworkType") != "VPC" ||
			q.Get("VPCId") != f.placement.VPC || q.Get("VSwitchId") != f.placement.Subnet || q.Get("ZoneId") != f.placement.Zone ||
			q.Get("PayType") != "Postpaid" || q.Get("Category") != "Basic" || q.Get("DBInstanceClass") != f.placement.Flavor ||
			q.Get("DBInstanceStorageType") != "cloud_essd" || q.Get("DBInstanceStorage") != "40" || q.Get("DeletionProtection") != "true" || len(q) != 15 {
			t.Error("clone request did not preserve the approved plan")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.writes.Add(1)
		if mode == alibabaRecoveryLostReceipt {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			if err := connection.Close(); err != nil {
				t.Error(err)
			}
			return
		}
	case "DescribeLocalAvailableRecoveryTime":
		if id != alibabaRecoverySource || q.Get("Region") != f.region || len(q) != 2 {
			t.Error("unscoped restore window request")
		}
	default:
		if len(q) != 1 {
			t.Error("unexpected RDS recovery observation parameters")
		}
	}
	testsupport.WriteJSON(t, w, f.response(t, action, id, mode))
}

func (f *alibabaDatabaseRecoveryFixture) response(t *testing.T, action, id string, mode int32) map[string]any {
	t.Helper()
	target := id == alibabaRecoveryTarget
	switch action {
	case "CloneDBInstance":
		return map[string]any{"DBInstanceId": alibabaRecoveryTarget, "RequestId": alibabaRecoveryJob, "OrderId": "1007893709999"}
	case "DescribeLocalAvailableRecoveryTime":
		return map[string]any{"DBInstanceId": id, "RecoveryBeginTime": f.point.Add(-time.Minute).Format(time.RFC3339),
			"RecoveryEndTime": f.point.Add(time.Minute).Format(time.RFC3339)}
	case "DescribeDBInstanceAttribute":
		return map[string]any{"Items": map[string]any{"DBInstanceAttribute": []any{f.attributes(id)}}}
	case "DescribeDBInstanceNetInfo":
		ipType := "Private"
		if (target && mode == alibabaRecoveryPublicTarget) || (!target && mode == alibabaRecoveryPublicSource) {
			ipType = "Public"
		}
		return map[string]any{"DBInstanceNetInfos": map[string]any{"DBInstanceNetInfo": []any{map[string]string{
			"DBInstanceId": id, "IPType": ipType, "ConnectionString": fixtureDatabaseHost,
		}}}}
	case "DescribeSecurityGroupConfiguration":
		groups := []map[string]string{}
		if !target && mode == alibabaRecoverySecurityLink {
			groups = append(groups, map[string]string{"SecurityGroupId": "sg-outside"})
		}
		return map[string]any{"Items": map[string]any{"EcsSecurityGroupRelation": groups}}
	case "DescribeDBInstanceIPArrayList":
		list := "172.16.8.0/24,127.0.0.1"
		if target {
			list = "127.0.0.1/32, 172.16.8.0/24"
		} else if mode == alibabaRecoveryOpenList {
			list = "10.0.0.0/7"
		}
		return map[string]any{"Items": map[string]any{"DBInstanceIPArray": []any{map[string]string{"SecurityIPList": list}}}}
	case "DescribeDBInstanceTDE":
		return map[string]any{"TDEStatus": "Disabled"}
	case "DescribeDBInstanceEncryptionKey":
		key := alibabaRecoveryKey
		if target && mode == alibabaRecoveryWrongKey {
			key = "163a3c6d-7d2a-4ec8-8abd-0273aef16585"
		}
		return map[string]any{"EncryptionKey": key, "EncryptionKeyStatus": "Enabled", "EncryptionKeyList": []any{map[string]string{
			"EncryptionKey": key, "EncryptionKeyStatus": "Enabled", "KeyType": "ServiceKey", "UsedBy": "DiskEncryption",
		}}}
	default:
		t.Errorf("unexpected RDS action %q", action)
		return nil
	}
}

func (f *alibabaDatabaseRecoveryFixture) attributes(id string) map[string]any {
	row := f.databaseRow(provider.AlibabaRDS, id, "source-db", "8.0", f.region)
	row["Category"], row["DBInstanceType"], row["InstanceNetworkType"] = "Basic", "Primary", "VPC"
	row["DBInstanceClass"], row["DBInstanceStorageType"], row["DBInstanceStorage"] = f.placement.Flavor, "cloud_essd", 30
	if id == alibabaRecoveryTarget {
		row["VSwitchId"], row["VpcId"], row["ZoneId"] = f.placement.Subnet, f.placement.VPC, f.placement.Zone
		row["DBInstanceDescription"], row["PayType"], row["DeletionProtection"], row["DBInstanceStorage"] = recoveryName, "Postpaid", true, 40
	}
	return row
}
