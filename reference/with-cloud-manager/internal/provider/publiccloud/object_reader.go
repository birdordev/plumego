package publiccloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

var bucketDNSName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
var cosBillingBucket = regexp.MustCompile(`-[0-9]{5,32}$`)

type ObjectCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
	SecurityToken   string `json:"security_token,omitempty"`
}

type ObjectReader struct {
	client               *Client
	bucket, region, host string
}

func NewObjectReader(name, region, bucket string, key ObjectCredentials, transport http.RoundTripper) (*ObjectReader, error) {
	if !bucketDNSName.MatchString(bucket) || len(region) > 40 || !regionID.MatchString(region) ||
		!credential(key.AccessKeyID, 128, false) || strings.ContainsAny(key.AccessKeyID, "&=:/") ||
		!credential(key.AccessKeySecret, 256, false) || !credential(key.SecurityToken, 8192, true) {
		return nil, errors.New("invalid object storage endpoint or credentials")
	}
	var host string
	switch name {
	case provider.TencentCVM:
		if !cosBillingBucket.MatchString(bucket) {
			return nil, errors.New("COS bucket name requires its APPID suffix")
		}
		host = bucket + ".cos." + region + ".myqcloud.com"
	case provider.AlibabaECS:
		host = bucket + ".oss-" + region + ".aliyuncs.com"
	case provider.HuaweiECS:
		host = bucket + ".obs." + region + ".myhuaweicloud.com"
	default:
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	client := &Client{name: name, profile: Config{AccessKeyID: key.AccessKeyID, AccessKeySecret: key.AccessKeySecret,
		SecurityToken: key.SecurityToken}, http: http.Client{Transport: transport, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	return &ObjectReader{client: client, bucket: bucket, region: region, host: host}, nil
}

func (s *ObjectReader) Head(ctx context.Context, key, match string) (provider.ObjectMeta, error) {
	res, err := s.open(ctx, http.MethodHead, key, match)
	if err != nil {
		return provider.ObjectMeta{}, err
	}
	defer func() { _ = res.Body.Close() }()
	return objectMeta(res, match)
}

func (s *ObjectReader) Read(ctx context.Context, key string, meta provider.ObjectMeta, limit int64) ([]byte, error) {
	if meta.Bytes < 1 || meta.Bytes > limit || limit > 64<<20 {
		return nil, provider.ErrObjectSize
	}
	if !strongObjectETag(meta.ETag) {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	res, err := s.open(ctx, http.MethodGet, key, meta.ETag)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if _, err := objectMeta(res, meta.ETag); err != nil {
		return nil, err
	}
	if res.ContentLength != meta.Bytes {
		return nil, provider.ErrObjectChanged
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, meta.Bytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &provider.Failure{Kind: provider.Transient}
	}
	if int64(len(raw)) != meta.Bytes {
		return nil, provider.ErrObjectChanged
	}
	return raw, nil
}

func (s *ObjectReader) open(ctx context.Context, method, key, match string) (*http.Response, error) {
	if !provider.ValidObjectKey(key) || match != "" && !strongObjectETag(match) {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	path := "/" + key
	endpoint := url.URL{Scheme: "https", Host: s.host, Path: path, RawPath: encodeObjectPath(path)}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("Accept-Encoding", "identity")
	if match != "" {
		req.Header.Set("If-Match", match)
	}
	now := time.Now().UTC()
	switch s.client.name {
	case provider.TencentCVM:
		s.client.signCOS(req, "", now)
	case provider.AlibabaECS:
		s.client.signOSS(req, s.region, "/"+s.bucket+req.URL.EscapedPath(), now)
	case provider.HuaweiECS:
		profile := s.client.profile
		signOBS(req, obsCredential{Access: profile.AccessKeyID, Secret: profile.AccessKeySecret, Token: profile.SecurityToken},
			"/"+s.bucket+req.URL.EscapedPath(), now)
	}
	res, err := s.client.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &provider.Failure{Kind: provider.Transient}
	}
	if res.StatusCode != http.StatusOK {
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode == http.StatusPreconditionFailed {
			return nil, provider.ErrObjectChanged
		}
		if res.StatusCode == http.StatusNotFound {
			return nil, &provider.Failure{Kind: provider.Absent}
		}
		return nil, cloudHTTPFailure(res)
	}
	return res, nil
}

func objectMeta(res *http.Response, match string) (provider.ObjectMeta, error) {
	etags := res.Header.Values("ETag")
	encoding := res.Header.Values("Content-Encoding")
	if len(encoding) > 1 || len(encoding) == 1 && !strings.EqualFold(encoding[0], "identity") ||
		res.ContentLength < 1 || len(etags) != 1 || !strongObjectETag(etags[0]) {
		return provider.ObjectMeta{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if match != "" && etags[0] != match {
		return provider.ObjectMeta{}, provider.ErrObjectChanged
	}
	return provider.ObjectMeta{ETag: etags[0], Bytes: res.ContentLength}, nil
}

func strongObjectETag(value string) bool {
	if len(value) < 3 || len(value) > 130 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	for _, c := range value[1 : len(value)-1] {
		if c < 0x21 || c > 0x7e || c == '"' {
			return false
		}
	}
	return true
}

func encodeObjectPath(path string) string {
	var escaped strings.Builder
	escaped.Grow(len(path))
	const hexDigits = "0123456789ABCDEF"
	for i := range len(path) {
		b := path[i]
		if b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || strings.IndexByte("-_.~/", b) >= 0 {
			escaped.WriteByte(b)
		} else {
			escaped.WriteByte('%')
			escaped.WriteByte(hexDigits[b>>4])
			escaped.WriteByte(hexDigits[b&15])
		}
	}
	return escaped.String()
}
