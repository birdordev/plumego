package publiccloud

import (
	"encoding/json"
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestHuaweiRebootRequiresCorrelatedTarget(t *testing.T) {
	cases := []struct {
		name, body string
		want       provider.ActionResult
	}{
		{"pending without a target", `{"job_id":"job-1","status":"INIT"}`, provider.ActionPending},
		{"direct target", `{"job_id":"job-1","status":"SUCCESS","entities":{"server_id":"target"}}`, provider.ActionSucceeded},
		{"failed target", `{"job_id":"job-1","status":"FAIL","entities":{"server_id":"target"}}`, provider.ActionFailed},
		{"unrelated job", `{"job_id":"job-2","status":"SUCCESS","entities":{"server_id":"target"}}`, ""},
		{"missing target", `{"job_id":"job-1","status":"SUCCESS"}`, ""},
		{"unrelated instance", `{"job_id":"job-1","status":"SUCCESS","entities":{"server_id":"foreign"}}`, ""},
		{"wrong child instance", `{"job_id":"job-1","status":"SUCCESS","entities":{"server_id":"target","sub_jobs_total":1,"sub_jobs":[{"status":"SUCCESS","entities":{"server_id":"foreign"}}]}}`, ""},
		{"disagreeing child", `{"job_id":"job-1","status":"SUCCESS","entities":{"sub_jobs":[{"status":"FAIL","entities":{"server_id":"target"}}]}}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var job huaweiInstanceJob
			if err := json.Unmarshal([]byte(c.body), &job); err != nil {
				t.Fatal(err)
			}
			got, err := job.result("target", "job-1", "", "")
			if c.want == "" {
				if !provider.IsFailure(err, provider.InvalidResponse) || got != "" {
					t.Fatalf("unverified job yielded %q, %v", got, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("job yielded %q, want %q, error %v", got, c.want, err)
			}
		})
	}
}

func TestHuaweiDeletionRequiresMatchingJobType(t *testing.T) {
	for _, tc := range []struct {
		name, parent, child string
		valid               bool
	}{
		{"matching types", "deleteVMs", "deleteVM", true},
		{"different parent action", "createServer", "deleteVM", false},
		{"different child action", "deleteVMs", "createSingleServer", false},
		{"missing parent action", "", "deleteVM", false},
		{"missing child action", "deleteVMs", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var job huaweiInstanceJob
			if err := json.Unmarshal([]byte(`{"job_id":"job-1","status":"SUCCESS","entities":{"sub_jobs_total":1,"sub_jobs":[{"status":"SUCCESS","entities":{"server_id":"target"}}]}}`), &job); err != nil {
				t.Fatal(err)
			}
			job.Type, job.Entities.Subjobs[0].Type = tc.parent, tc.child
			got, err := job.result("target", "job-1", "deleteVMs", "deleteVM")
			if tc.valid {
				if err != nil || got != provider.ActionSucceeded {
					t.Fatalf("verified deletion yielded %q, %v", got, err)
				}
			} else if got != "" || !provider.IsFailure(err, provider.InvalidResponse) {
				t.Fatalf("unverified deletion yielded %q, %v", got, err)
			}
		})
	}
}
