package cloudtest

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/app/integration/testsupport"
)

const (
	fixtureHuaweiProject = "abcdef1234567890abcdef1234567890"
	fixtureHuaweiIAM     = "fixture-iam-create"
	fixtureHuaweiFirst   = "10000000-0000-4000-8000-000000000001"
	fixtureHuaweiSecond  = "10000000-0000-4000-8000-000000000002"
	fixtureHuaweiNetwork = "20000000-0000-4000-8000-000000000001"
	fixtureHuaweiIPv4    = "20000000-0000-4000-8000-000000000002"
	fixtureHuaweiVPC     = "20000000-0000-4000-8000-000000000003"
	fixtureHuaweiPort    = "20000000-0000-4000-8000-000000000004"
	fixtureHuaweiImage   = "30000000-0000-4000-8000-000000000001"
	fixtureHuaweiGroup   = "30000000-0000-4000-8000-000000000002"
)

func (f *publicCloudCreationFixture) huawei(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	send := func(value any) { testsupport.WriteJSON(t, w, value) }
	if r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	if r.Method == http.MethodGet && r.Host == "iam.myhuaweicloud.com" && r.URL.Path == "/v3/auth/tokens" &&
		r.Header.Get("X-Subject-Token") == fixtureHuaweiIAM && r.URL.Query().Get("nocatalog") == "true" {
		send(map[string]any{"token": map[string]any{"expires_at": time.Now().UTC().Add(time.Hour),
			"project": map[string]any{"id": fixtureHuaweiProject, "domain": map[string]string{"id": f.account}}}})
		return true
	}
	base := "/v1/" + fixtureHuaweiProject
	const zone = "cn-north-4a"
	if r.Method == http.MethodGet && r.Host == "vpc."+f.region+".myhuaweicloud.com" && r.URL.Path == base+"/subnets/"+fixtureHuaweiNetwork {
		if f.revokeOnRead.CompareAndSwap(true, false) {
			if err := f.revoke(r.Context()); err != nil {
				t.Error(err)
				return false
			}
		}
		send(map[string]any{"subnet": map[string]string{"id": fixtureHuaweiNetwork, "status": "ACTIVE", "tenant_id": fixtureHuaweiProject,
			"vpc_id": fixtureHuaweiVPC, "availability_zone": zone, "neutron_network_id": fixtureHuaweiNetwork, "neutron_subnet_id": fixtureHuaweiIPv4}})
		return true
	}
	if r.Host != "ecs."+f.region+".myhuaweicloud.com" {
		return false
	}
	collection := "/v1.1/" + fixtureHuaweiProject + "/cloudservers"
	if r.Method == http.MethodPost && r.URL.Path == collection {
		if r.Header.Get("Content-Type") != "application/json" || !f.recordHuaweiCreation(t, r) {
			return false
		}
		if f.writes.Load() == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			send(map[string]any{"job_id": "fixture-launch-receipt", "serverIds": []string{f.currentInstance()}})
		}
		return true
	}
	marker := f.marker.Load()
	if r.Method != http.MethodGet || marker == nil {
		return false
	}
	instanceID := f.currentInstance()
	instance := base + "/cloudservers/" + instanceID
	switch r.URL.Path {
	case collection + "/detail":
		if f.writes.Load() != 1 || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("tags") != fixtureLaunchTag+"="+*marker {
			return false
		}
		send(map[string]any{"servers": []map[string]string{{"id": instanceID, "tenant_id": fixtureHuaweiProject}}})
	case base + "/jobs/fixture-launch-receipt":
		if f.writes.Load() != 2 {
			return false
		}
		job := map[string]any{"job_id": "fixture-launch-receipt", "job_type": "createServer", "status": "RUNNING"}
		if !f.jobPending.Load() {
			job["status"] = "SUCCESS"
			job["entities"] = map[string]any{"sub_jobs_total": 1, "sub_jobs": []map[string]any{
				{"status": "SUCCESS", "job_type": "createSingleServer", "entities": map[string]string{"server_id": instanceID}},
			}}
		}
		send(job)
	case instance:
		status := "BUILD"
		if f.ready.Load() {
			status = "ACTIVE"
		}
		send(map[string]any{"server": map[string]any{"id": instanceID, "name": f.spec.Name, "tenant_id": fixtureHuaweiProject,
			"status": status, "image": map[string]string{"id": f.spec.ImageID}, "flavor": map[string]string{"id": f.spec.Size},
			"metadata": map[string]string{"vpc_id": fixtureHuaweiVPC, "charging_mode": "0"}, "OS-EXT-AZ:availability_zone": zone,
			"security_groups": []map[string]string{{"id": f.spec.SecurityGroup}},
		}})
	case instance + "/tags":
		send(map[string]any{"tags": []map[string]string{{"key": fixtureLaunchTag, "value": *marker}}})
	case instance + "/os-interface":
		subnet := fixtureHuaweiIPv4
		if f.wrongResult.Load() {
			subnet = fixtureHuaweiVPC
		}
		send(map[string]any{"interfaceAttachments": []map[string]any{
			{"port_state": "ACTIVE", "net_id": fixtureHuaweiNetwork, "port_id": fixtureHuaweiPort,
				"fixed_ips": []map[string]string{{"subnet_id": subnet, "ip_address": "10.0.0.10"}}},
		}})
	default:
		return false
	}
	return true
}

func (f *publicCloudCreationFixture) recordHuaweiCreation(t *testing.T, r *http.Request) bool {
	t.Helper()
	var payload struct {
		Server struct {
			Name, ImageRef, FlavorRef, Vpcid string
			Count                            int
			Zone                             string                        `json:"availability_zone"`
			Root                             struct{ Volumetype string }   `json:"root_volume"`
			Extended                         struct{ ChargingMode string } `json:"extendparam"`
			Tags                             []struct{ Key, Value string } `json:"server_tags"`
			NICs                             []struct {
				SubnetID string `json:"subnet_id"`
			} `json:"nics"`
			SecurityGroups []struct{ ID string } `json:"security_groups"`
		}
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&payload) != nil {
		return false
	}
	s := payload.Server
	if s.Name != f.spec.Name || s.ImageRef != f.spec.ImageID || s.FlavorRef != f.spec.Size || s.Vpcid != fixtureHuaweiVPC ||
		s.Count != 1 || s.Zone != "cn-north-4a" || s.Root.Volumetype != "SSD" || s.Extended.ChargingMode != "postPaid" ||
		len(s.Tags) != 1 || s.Tags[0].Key != fixtureLaunchTag || len(s.Tags[0].Value) != 64 ||
		len(s.NICs) != 1 || s.NICs[0].SubnetID != fixtureHuaweiNetwork ||
		len(s.SecurityGroups) != 1 || s.SecurityGroups[0].ID != f.spec.SecurityGroup {
		return false
	}
	marker := s.Tags[0].Value
	token, err := f.persistedToken(r.Context())
	if err != nil {
		t.Error(err)
		return false
	}
	expectedMarker := sha256.Sum256([]byte(strings.Join([]string{fixtureLaunchTag, f.provider, f.account + ":" + fixtureHuaweiProject, f.region, token}, "\x00")))
	digest := sha256.Sum256([]byte("huaweicloud-ecs-client-token\x00" + token))
	canonical := fmt.Sprintf("%x-%x-%04x-%04x-%x", digest[:4], digest[4:6],
		0x4000|uint16(digest[6]&0xf)<<8|uint16(digest[7]), 0x8000|uint16(digest[8]&0x3f)<<8|uint16(digest[9]), digest[10:16])
	if r.Header.Get("X-Client-Token") != canonical || marker != fmt.Sprintf("%x", expectedMarker) {
		return false
	}
	f.marker.Store(&marker)
	f.writes.Add(1)
	return true
}
