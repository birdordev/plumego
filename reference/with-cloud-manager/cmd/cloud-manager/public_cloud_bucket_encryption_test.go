package main

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func testPublicCloudBucketEncryption(t *testing.T, db *sql.DB, app http.Handler, f *bucketFixture, actor, id, profile string, transport http.RoundTripper) string {
	t.Helper()
	args := []string{"refresh-" + f.command + "-bucket-encryption", "tenant", f.vendor, id}
	if name, _ := publicCloudCommand(args[0]); !validCommand(args) || name != f.vendor {
		t.Fatalf("bucket encryption command is not wired: %q", args[0])
	}
	path := "/v1/tenants/tenant/projects/project/assets/object-buckets/" + url.PathEscape(id) + "/encryption?account=" + url.QueryEscape(f.vendor)
	refresh := func() error { return runPublicCloud(t.Context(), db, args, f.vendor, profile, transport) }
	read := func(code int) inventory.BucketEncryptionSnapshot {
		t.Helper()
		response := scheduleResponse(t, app, http.MethodGet, path, actor, "", "", code)
		var snapshot inventory.BucketEncryptionSnapshot
		if code == http.StatusOK {
			decodeProjectData(t, response, &snapshot)
			for _, hidden := range []string{fixtureBucketKeyID, "discarded-project", "KMSMasterKeyID", "BucketKeyEnabled"} {
				if strings.Contains(response.Body.String(), hidden) {
					t.Fatalf("bucket encryption fixture exposed irrelevant details: %q", hidden)
				}
			}
		}
		return snapshot
	}
	read(http.StatusNotFound)
	scheduleResponse(t, app, http.MethodGet, path+"&other=unexpected", actor, "", "", http.StatusBadRequest)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	first := read(http.StatusOK)
	if first.State != provider.BucketEncryptionKMS || first.ObservedAt.IsZero() {
		t.Fatalf("invalid first bucket encryption snapshot: %+v", first)
	}
	scheduleResponse(t, app, http.MethodGet, strings.Replace(path, "/project/", "/other/", 1), actor, "", "", http.StatusNotFound)
	type rejectedMode struct {
		mode int32
		kind provider.FailureKind
	}
	failedModes := []rejectedMode{
		{1, provider.Unauthorized}, {3, provider.OutcomeUnknown}, {4, provider.Throttled},
		{5, provider.OutcomeUnknown}, {8, provider.InvalidResponse}, {9, provider.InvalidResponse},
	}
	if f.vendor == provider.HuaweiECS {
		failedModes = append(failedModes, rejectedMode{7, provider.InvalidResponse})
	}
	for _, failure := range failedModes {
		f.encryptionMode.Store(failure.mode)
		err := refresh()
		f.recreated.Store(false)
		if !provider.IsFailure(err, failure.kind) || strings.Contains(err.Error(), fixtureBucketKeyID) {
			t.Fatalf("bucket encryption mode %d returned %v", failure.mode, err)
		}
		if latest := read(http.StatusOK); latest.State != first.State || !latest.ObservedAt.Equal(first.ObservedAt) {
			t.Fatalf("failed refresh replaced bucket encryption: %+v", latest)
		}
	}
	type acceptedMode struct {
		mode  int32
		state provider.BucketEncryption
	}
	goodModes := []acceptedMode{{2, provider.BucketEncryptionUnconfigured}, {6, provider.BucketEncryptionAES256}}
	if f.vendor != provider.HuaweiECS {
		goodModes = append(goodModes, acceptedMode{7, provider.BucketEncryptionSM4})
	}
	for _, accepted := range goodModes {
		f.encryptionMode.Store(accepted.mode)
		if err := refresh(); err != nil {
			t.Fatal(err)
		}
		if snapshot := read(http.StatusOK); snapshot.State != accepted.state {
			t.Fatalf("bucket encryption mode %d returned %+v", accepted.mode, snapshot)
		}
	}
	f.encryptionMode.Store(0)
	return path
}
