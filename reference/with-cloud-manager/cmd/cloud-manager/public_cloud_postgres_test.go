package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

type cloudFixtureTransport struct {
	endpoint  *url.URL
	transport http.RoundTripper
}

func (f cloudFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	req := r.Clone(r.Context())
	req.Host = req.URL.Host
	req.URL.Host = f.endpoint.Host
	return f.transport.RoundTrip(req)
}

func TestAcceptancePublicCloudInventory(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const bearer = "fixture-public-cloud-account-administrator"
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','admin','service','fixture','admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES ('tenant','project','admin','admin');`); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(bearer))
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'tenant','admin',now()+interval '10 minutes')`, digest[:]); err != nil {
		t.Fatal(err)
	}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const domain = "1234567890abcdef1234567890abcdef"
	const project = "abcdef1234567890abcdef1234567890"
	const firstHuawei = "00000000-0000-4000-8000-000000000001"
	const secondHuawei = "00000000-0000-4000-8000-000000000002"
	const sharedToken = "fixture-temporary-session-token"
	const key = "fixture-access-key"
	const secret = "fixture-access-secret"
	const huaweiToken = "fixture-huawei-iam-token"
	const nextAli = "fixture+/=cursor"
	for _, item := range []struct{ name, cli, account, region, cursor, identity string }{
		{provider.TencentCVM, "tencent", "100000000001", "ap-guangzhou", "1", "100000000001"},
		{provider.AlibabaECS, "alibaba", "1000000000000002", "cn-hangzhou", nextAli, "1000000000000002"},
		{provider.HuaweiECS, "huawei", domain, "cn-north-4", firstHuawei, domain + ":" + project},
	} {
		t.Run(item.cli, func(t *testing.T) {
			var identityWrong, redirect atomic.Bool
			var firstPage, secondPage atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				write := func(body string) {
					if _, err := io.WriteString(w, body); err != nil {
						t.Error(err)
					}
				}
				account := item.account
				if identityWrong.Load() {
					account = "777777777777"
				}
				var resumed bool
				switch r.Host {
				case "sts.tencentcloudapi.com", "cvm.tencentcloudapi.com":
					body, err := io.ReadAll(r.Body)
					if err != nil || !validFixtureTencentSignature(r, body, key, secret) || r.Header.Get("X-TC-Region") != item.region || r.Header.Get("X-TC-Token") != sharedToken {
						t.Error("invalid Tencent request signature or scope")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if r.Header.Get("X-TC-Action") == "GetCallerIdentity" {
						write(fmt.Sprintf(`{"Response":{"AccountId":%q,"PrincipalId":"123"}}`, account))
						return
					}
					var page struct{ Offset, Limit int }
					if json.Unmarshal(body, &page) != nil || page.Limit != 100 || (page.Offset != 0 && page.Offset != 1) || r.Header.Get("X-TC-Action") != "DescribeInstances" {
						t.Error("invalid Tencent page request")
					}
					resumed = page.Offset == 1
				case "sts.aliyuncs.com", "ecs.cn-hangzhou.aliyuncs.com":
					if !validFixtureAlibabaSignature(r, key, secret) || r.Header.Get("X-ACS-Security-Token") != sharedToken {
						t.Error("invalid Alibaba request signature")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if r.Header.Get("X-ACS-Action") == "GetCallerIdentity" {
						write(fmt.Sprintf(`{"AccountId":%q}`, account))
						return
					}
					q := r.URL.Query()
					resumed = q.Get("NextToken") != ""
					if r.Header.Get("X-ACS-Action") != "DescribeInstances" || (resumed && q.Get("NextToken") != nextAli) || q.Get("RegionId") != item.region || q.Get("MaxResults") != "100" {
						t.Error("invalid Alibaba page request")
					}
				case "iam.myhuaweicloud.com", "ecs.cn-north-4.myhuaweicloud.com":
					if r.Method != http.MethodGet || r.Header.Get("X-Auth-Token") != huaweiToken {
						t.Error("missing Huawei project token")
					}
					if r.URL.Path == "/v3/auth/tokens" {
						if r.Header.Get("X-Subject-Token") != huaweiToken {
							t.Error("incorrect subject token")
						}
						write(fmt.Sprintf(`{"token":{"expires_at":%q,"project":{"id":%q,"domain":{"id":%q}}}}`, time.Now().UTC().Add(time.Hour).Format(time.RFC3339), project, account))
						return
					}
					q := r.URL.Query()
					resumed = q.Get("marker") != ""
					if r.URL.Path != "/v1.1/"+project+"/cloudservers/detail" || q.Get("limit") != "100" || (resumed && q.Get("marker") != firstHuawei) {
						t.Error("invalid Huawei project or marker")
					}
				default:
					t.Errorf("unexpected endpoint %s", r.Host)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if redirect.Load() {
					w.Header().Set("Location", "https://incorrect.example/forwarded")
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				}
				if resumed && secondPage.Add(1) == 1 {
					w.Header().Set("Retry-After", "5")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				position := 1
				if resumed {
					position = 2
				} else {
					firstPage.Add(1)
				}
				switch item.name {
				case provider.TencentCVM:
					write(fmt.Sprintf(`{"Response":{"TotalCount":2,"InstanceSet":[{"InstanceId":"ins-%d","InstanceName":"fixture-cvm","InstanceState":"RUNNING","VirtualPrivateCloud":{"VpcId":"vpc-1"}}]}}`, position))
				case provider.AlibabaECS:
					next := nextAli
					if resumed {
						next = ""
					}
					write(fmt.Sprintf(`{"NextToken":%q,"Instances":{"Instance":[{"InstanceId":"i-%d","InstanceName":"fixture-ecs","Status":"Running","RegionId":%q}]}}`, next, position, item.region))
				case provider.HuaweiECS:
					id, links := firstHuawei, ""
					if resumed {
						id = secondHuawei
					} else {
						links = fmt.Sprintf(`,"servers_links":[{"rel":"next","href":%q}]`, "?limit=100&marker="+firstHuawei)
					}
					write(fmt.Sprintf(`{"servers":[{"id":%q,"tenant_id":%q,"name":"fixture-server","status":"ACTIVE"}]%s}`, id, project, links))
				}
			}))
			defer server.Close()
			endpoint, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			transport := cloudFixtureTransport{endpoint, server.Client().Transport}
			profile := publiccloud.Config{Regions: []string{item.region}, AccountID: item.account}
			if item.name == provider.HuaweiECS {
				profile.ProjectID, profile.IAMToken = project, huaweiToken
			} else {
				profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = key, secret, sharedToken
			}
			ref := "fixture/" + item.cli
			path := filepath.Join(t.TempDir(), "profile.json")
			encoded, err := json.Marshal(struct {
				Ref string `json:"credential_ref"`
				publiccloud.Config
			}{ref, profile})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			const base = "/v1/tenants/tenant/projects/project"
			registered := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, base+"/cloud-accounts", strings.NewReader(fmt.Sprintf(`{"id":%q,"provider":%q,"credential_ref":%q}`, item.cli, item.name, ref)))
			request.Header.Set("Authorization", "Bearer "+bearer)
			request.Header.Set("Content-Type", "application/json")
			app.ServeHTTP(registered, request)
			if registered.Code != http.StatusCreated {
				t.Fatalf("account registration status %d: %s", registered.Code, registered.Body)
			}
			check := []string{"verify-" + item.cli, "tenant", item.cli}
			scan := []string{"scan-" + item.cli, "tenant", item.cli, item.region, "instance"}
			if !validCommand(check) || !validCommand(scan) {
				t.Fatal("commands are not registered")
			}
			identityWrong.Store(true)
			if err := runPublicCloud(ctx, db, check, item.name, path, transport); !provider.IsFailure(err, provider.Unauthorized) {
				t.Fatalf("foreign identity accepted: %v", err)
			}
			identityWrong.Store(false)
			if err := runPublicCloud(ctx, db, check, item.name, path, transport); err != nil {
				t.Fatal(err)
			}
			var remote, mode string
			if err := db.QueryRowContext(ctx, "SELECT remote_identity,mode FROM cmp_cloud_accounts WHERE account_id=$1", item.cli).Scan(&remote, &mode); err != nil || remote != item.identity || mode != "read_only" {
				t.Fatalf("incorrect account binding: %s %s %v", remote, mode, err)
			}
			redirect.Store(true)
			if err := runPublicCloud(ctx, db, scan, item.name, path, transport); !provider.IsFailure(err, provider.InvalidResponse) {
				t.Fatalf("redirect accepted: %v", err)
			}
			redirect.Store(false)
			var throttled *provider.Failure
			err = runPublicCloud(ctx, db, scan, item.name, path, transport)
			if !errors.As(err, &throttled) || throttled.Kind != provider.Throttled || throttled.RetryAfter != 5*time.Second {
				t.Fatalf("throttle classification: %v", err)
			}
			var cursor string
			if err := db.QueryRowContext(ctx, "SELECT cursor FROM cmp_inventory_scopes WHERE account_id=$1 AND kind='instance'", item.cli).Scan(&cursor); err != nil || cursor != item.cursor {
				t.Fatalf("checkpoint mismatch: %q %v", cursor, err)
			}
			if err := runPublicCloud(ctx, db, scan, item.name, path, transport); err != nil {
				t.Fatal(err)
			}
			if firstPage.Load() != 1 || secondPage.Load() != 2 {
				t.Fatalf("did not resume from committed checkpoint: %d %d", firstPage.Load(), secondPage.Load())
			}
			w := httptest.NewRecorder()
			request = httptest.NewRequest(http.MethodGet, base+"/assets?kind=instance&account="+item.cli, nil)
			request.Header.Set("Authorization", "Bearer "+bearer)
			app.ServeHTTP(w, request)
			var assets struct{ Data inventory.Assets }
			if err := json.Unmarshal(w.Body.Bytes(), &assets); err != nil || w.Code != http.StatusOK || len(assets.Data.Items) != 2 || assets.Data.Items[0].Provider != item.name {
				t.Fatalf("asset API mismatch %d %s: %v", w.Code, w.Body, err)
			}
		})
	}
}

func fixtureDigest(value []byte) string {
	checksum := sha256.Sum256(value)
	return hex.EncodeToString(checksum[:])
}
func fixtureHMAC(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = io.WriteString(h, value)
	return h.Sum(nil)
}

func validFixtureTencentSignature(r *http.Request, body []byte, key, secret string) bool {
	if r.Method != http.MethodPost || r.URL.Path != "/" {
		return false
	}
	timestamp, err := strconv.ParseInt(r.Header.Get("X-TC-Timestamp"), 10, 64)
	if err != nil {
		return false
	}
	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	service, _, _ := strings.Cut(r.Host, ".")
	scope := fmt.Sprintf("%s/%s/tc3_request", date, service)
	canonical := fmt.Sprintf("POST\n/\n\ncontent-type:%s\nhost:%s\n\ncontent-type;host\n%s", r.Header.Get("Content-Type"), r.Host, fixtureDigest(body))
	message := strings.Join([]string{"TC3-HMAC-SHA256", r.Header.Get("X-TC-Timestamp"), scope, fixtureDigest([]byte(canonical))}, "\n")
	sig := fixtureHMAC(fixtureHMAC(fixtureHMAC(fixtureHMAC([]byte("TC3"+secret), date), service), "tc3_request"), message)
	return r.Header.Get("Authorization") == fmt.Sprintf("TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=content-type;host, Signature=%x", key, scope, sig)
}

func validFixtureAlibabaSignature(r *http.Request, key, secret string) bool {
	if r.Method != http.MethodPost || r.URL.Path != "/" || r.Header.Get("X-ACS-Signature-Nonce") == "" {
		return false
	}
	const signed = "host;x-acs-action;x-acs-content-sha256;x-acs-date;x-acs-security-token;x-acs-signature-nonce;x-acs-version"
	var headers strings.Builder
	fmt.Fprintf(&headers, "host:%s\n", r.Host)
	for _, name := range strings.Split(signed, ";")[1:] {
		fmt.Fprintf(&headers, "%s:%s\n", name, r.Header.Get(name))
	}
	canonical := fmt.Sprintf("POST\n/\n%s\n%s\n%s\n%s", r.URL.RawQuery, headers.String(), signed, fixtureDigest(nil))
	sig := fixtureHMAC([]byte(secret), "ACS3-HMAC-SHA256\n"+fixtureDigest([]byte(canonical)))
	return r.Header.Get("Authorization") == fmt.Sprintf("ACS3-HMAC-SHA256 Credential=%s,SignedHeaders=%s,Signature=%x", key, signed, sig)
}
