package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/provider"
)

const (
	fixtureBucketOld  = "2025-01-01T10:00:00Z"
	fixtureBucketNew  = "2025-01-02T10:00:00Z"
	fixtureBucketHost = "discarded-bucket-connection.example.test"
	fixtureOBSAccess  = "obs-fixture-access"
	fixtureOBSSecret  = "obs-fixture-secret"
	fixtureOBSToken   = "obs-fixture-security-token"
)

type bucketFixture struct {
	vpcCreationFixture
	command, product, name, location string
	firstReads                       atomic.Int32
	mode                             atomic.Int32
	recreated                        atomic.Bool
}

func (f *bucketFixture) serveBuckets(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" && f.tencent(t, w, r) ||
		r.Host == "sts.aliyuncs.com" && f.alibaba(t, w, r) ||
		r.URL.Path == "/v3/auth/tokens" && f.huawei(t, w, r) {
		return
	}

	if f.vendor == provider.HuaweiECS && r.Host == "iam.myhuaweicloud.com" && r.URL.Path == "/v3.0/OS-CREDENTIAL/securitytokens" {
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != http.MethodPost || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM ||
			!strings.Contains(string(body), `"duration_seconds":900`) ||
			!strings.Contains(string(body), `"Action":["obs:bucket:ListAllMyBuckets"]`) {
			t.Error("invalid OBS temporary credential request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeCloudFixtureJSON(t, w, map[string]any{"credential": map[string]string{
			"access": fixtureOBSAccess, "secret": fixtureOBSSecret, "securitytoken": fixtureOBSToken,
			"expires_at": time.Now().Add(14 * time.Minute).UTC().Format(time.RFC3339),
		}})
		return
	}

	query := r.URL.Query()
	marker := query.Get("marker")
	valid := r.Method == http.MethodGet && r.URL.Path == "/"
	if f.vendor != provider.HuaweiECS {
		valid = valid && query.Get("max-keys") == "100" && (marker == "" || marker == "fixture-next")
	}
	switch f.vendor {
	case provider.TencentCVM:
		valid = valid && r.Host == "service.cos.myqcloud.com" && validFixtureCOSBucketSignature(r)
	case provider.AlibabaECS:
		valid = valid && r.Host == "oss-"+f.region+".aliyuncs.com" && validFixtureOSSBucketSignature(r, f.region)
	case provider.HuaweiECS:
		valid = valid && r.Host == "obs."+f.region+".myhuaweicloud.com" && r.URL.RawQuery == "" && validFixtureOBSBucketSignature(r)
	}
	if !valid {
		t.Errorf("unexpected object bucket request (%s, %s)", r.Method, r.Host)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if marker == "" {
		f.firstReads.Add(1)
	}
	mode := f.mode.Load()
	if mode == 1 && (marker != "" || f.vendor == provider.HuaweiECS) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if mode == 3 && (marker != "" || f.vendor == provider.HuaweiECS) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	owner := f.account
	if f.vendor == provider.TencentCVM {
		owner = "qcs::cam::uin/" + owner + ":uin/" + owner
	}
	if mode == 2 {
		owner = "wrong-scope-owner"
	}
	created := fixtureBucketOld
	if f.recreated.Load() {
		created = fixtureBucketNew
	}
	name, next := f.name, ""
	if marker != "" {
		name = strings.Replace(name, "first", "second", 1)
		created = fixtureBucketOld
	} else if f.vendor != provider.HuaweiECS {
		next = "fixture-next"
	}
	location, region, kind, class := f.location, "", "", ""
	switch f.vendor {
	case provider.TencentCVM:
		kind = "cos"
	case provider.AlibabaECS:
		location, region, class = "oss-"+location, f.location, "Standard"
	case provider.HuaweiECS:
		kind = "OBJECT"
	}
	w.Header().Set("Content-Type", "application/xml")
	_, err := fmt.Fprintf(w, `<?xml version="1.0"?>
<ListAllMyBucketsResult><Owner><ID>%s</ID><DisplayName>%s</DisplayName></Owner>
  <Marker>%s</Marker><IsTruncated>%t</IsTruncated><NextMarker>%s</NextMarker>
  <Buckets><Bucket><Name>%s</Name><Location>%s</Location><Region>%s</Region>
    <CreationDate>%s</CreationDate><BucketType>%s</BucketType><StorageClass>%s</StorageClass>
	<ExtranetEndpoint>%s</ExtranetEndpoint></Bucket></Buckets></ListAllMyBucketsResult>`,
		owner, fixtureBucketHost, marker, next != "", next, name, location, region, created, kind, class, fixtureBucketHost)
	if err != nil {
		t.Error(err)
	}
}

func fixtureBucketMAC(algorithm func() hash.Hash, key []byte, payload string) []byte {
	h := hmac.New(algorithm, key)
	_, _ = h.Write([]byte(payload))
	return h.Sum(nil)
}

func validFixtureCOSBucketSignature(r *http.Request) bool {
	queryNames := "max-keys"
	if r.URL.Query().Has("marker") {
		queryNames = "marker;max-keys"
	}
	return validFixtureCOSSignature(r, queryNames)
}

func validFixtureCOSSignature(r *http.Request, queryNames string) bool {
	auth := make(url.Values)
	for _, pair := range strings.Split(r.Header.Get("Authorization"), "&") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || auth.Has(key) {
			return false
		}
		auth.Set(key, value)
	}
	window := auth.Get("q-sign-time")
	start, end, found := strings.Cut(window, ";")
	first, firstErr := strconv.ParseInt(start, 10, 64)
	last, lastErr := strconv.ParseInt(end, 10, 64)
	now := time.Now().Unix()
	if !found || firstErr != nil || lastErr != nil || first > now || now >= last || last-first > 310 ||
		auth.Get("q-ak") != fixtureLaunchKey || auth.Get("q-key-time") != window ||
		auth.Get("q-sign-algorithm") != "sha1" || r.Header.Get("X-Cos-Security-Token") != fixtureLaunchSession ||
		auth.Get("q-header-list") != "host;x-cos-security-token" {
		return false
	}
	hash := sha1.Sum([]byte(strings.ToLower(r.Method) + "\n" + r.URL.Path + "\n" + r.URL.RawQuery + "\nhost=" + r.Host + "&x-cos-security-token=" + url.QueryEscape(fixtureLaunchSession) + "\n"))
	key := []byte(hex.EncodeToString(fixtureBucketMAC(sha1.New, []byte(fixtureLaunchSecret), window)))
	want := hex.EncodeToString(fixtureBucketMAC(sha1.New, key, "sha1\n"+window+"\n"+hex.EncodeToString(hash[:])+"\n"))
	return auth.Get("q-url-param-list") == queryNames && hmac.Equal([]byte(want), []byte(auth.Get("q-signature")))
}

func validFixtureOSSBucketSignature(r *http.Request, region string) bool {
	return validFixtureOSSSignature(r, region, "/")
}

func validFixtureOSSSignature(r *http.Request, region, resource string) bool {
	stamp := r.Header.Get("X-Oss-Date")
	date, err := time.Parse("20060102T150405Z", stamp)
	if err != nil || time.Since(date).Abs() > time.Minute || r.Header.Get("X-Oss-Content-Sha256") != "UNSIGNED-PAYLOAD" ||
		r.Header.Get("X-Oss-Security-Token") != fixtureLaunchSession {
		return false
	}
	signed := r.Method + "\n" + resource + "\n" + r.URL.RawQuery + "\nhost:" + r.Host + "\nx-oss-content-sha256:UNSIGNED-PAYLOAD\nx-oss-date:" +
		stamp + "\nx-oss-security-token:" + fixtureLaunchSession + "\n\nhost\nUNSIGNED-PAYLOAD"
	hash := sha256.Sum256([]byte(signed))
	scope := date.Format("20060102") + "/" + region + "/oss/aliyun_v4_request"
	key := []byte("aliyun_v4" + fixtureLaunchSecret)
	for _, component := range strings.Split(scope, "/") {
		key = fixtureBucketMAC(sha256.New, key, component)
	}
	signature := hex.EncodeToString(fixtureBucketMAC(sha256.New, key, "OSS4-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+hex.EncodeToString(hash[:])))
	want := "OSS4-HMAC-SHA256 Credential=" + fixtureLaunchKey + "/" + scope + ",AdditionalHeaders=host,Signature=" + signature
	return hmac.Equal([]byte(want), []byte(r.Header.Get("Authorization")))
}

func validFixtureOBSBucketSignature(r *http.Request) bool {
	date := r.Header.Get("Date")
	stamp, err := http.ParseTime(date)
	if err != nil || time.Since(stamp).Abs() > time.Minute || r.Header.Get("X-Auth-Token") != "" ||
		r.Header.Get("X-Obs-Security-Token") != fixtureOBSToken || r.Header.Get("X-Obs-Bucket-Type") != "OBJECT" {
		return false
	}
	signed := "GET\n\n\n" + date + "\nx-obs-bucket-type:OBJECT\nx-obs-security-token:" + fixtureOBSToken + "\n/"
	want := "OBS " + fixtureOBSAccess + ":" + base64.StdEncoding.EncodeToString(fixtureBucketMAC(sha1.New, []byte(fixtureOBSSecret), signed))
	return hmac.Equal([]byte(want), []byte(r.Header.Get("Authorization")))
}
