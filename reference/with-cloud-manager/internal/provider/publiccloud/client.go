package publiccloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

const pageSize = 100

var (
	regionID   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+){1,6}$`)
	accountID  = regexp.MustCompile(`^[0-9]{1,32}$`)
	projectID  = regexp.MustCompile(`^[a-f0-9]{32}$`)
	instanceID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
)

type Config struct {
	Regions         []string `json:"regions"`
	AccountID       string   `json:"account_id"`
	ProjectID       string   `json:"project_id,omitempty"`
	AccessKeyID     string   `json:"access_key_id,omitempty"`
	AccessKeySecret string   `json:"access_key_secret,omitempty"`
	SecurityToken   string   `json:"security_token,omitempty"`
	IAMToken        string   `json:"iam_token,omitempty"`
}

type Client struct {
	name, identity string
	profile        Config
	http           http.Client
}

func Connect(ctx context.Context, name string, profile Config, transport http.RoundTripper) (*Client, error) {
	if len(profile.Regions) == 0 || len(profile.Regions) > 64 {
		return nil, errors.New("invalid cloud regions")
	}
	profile.Regions = slices.Clone(profile.Regions)
	for i, region := range profile.Regions {
		if len(region) > 40 || !regionID.MatchString(region) || slices.Contains(profile.Regions[:i], region) {
			return nil, errors.New("invalid cloud regions")
		}
	}
	c := &Client{name: name, profile: profile, http: http.Client{Transport: transport, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	switch name {
	case provider.TencentCVM, provider.AlibabaECS:
		if !accountID.MatchString(profile.AccountID) || profile.ProjectID != "" || profile.IAMToken != "" ||
			!credential(profile.AccessKeyID, 128, false) || !credential(profile.AccessKeySecret, 256, false) || !credential(profile.SecurityToken, 8192, true) {
			return nil, errors.New("invalid cloud signing credentials")
		}
		var found string
		var err error
		if name == provider.TencentCVM {
			found, err = c.tencentIdentity(ctx)
		} else {
			found, err = c.alibabaIdentity(ctx)
		}
		if err != nil {
			return nil, err
		}
		if found != profile.AccountID {
			return nil, &provider.Failure{Kind: provider.Unauthorized}
		}
		c.identity = found
	case provider.HuaweiECS:
		if len(profile.Regions) != 1 || !projectID.MatchString(profile.AccountID) || !projectID.MatchString(profile.ProjectID) ||
			!credential(profile.IAMToken, 8192, false) || profile.AccessKeyID != "" || profile.AccessKeySecret != "" || profile.SecurityToken != "" {
			return nil, errors.New("invalid Huawei IAM project credentials")
		}
		if err := c.verifyHuawei(ctx); err != nil {
			return nil, err
		}
		c.identity = profile.AccountID + ":" + profile.ProjectID
	default:
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	return c, nil
}

func credential(value string, maxLength int, optional bool) bool {
	if len(value) > maxLength || (value == "" && !optional) {
		return false
	}
	for i := range len(value) {
		if value[i] <= ' ' || value[i] >= 127 || value[i] == ',' {
			return false
		}
	}
	return true
}

func (c *Client) Identity() string { return c.identity }

func (c *Client) Discover(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind == provider.Region && scope.Region == "" && cursor == "" {
		page := provider.Page{Objects: make([]provider.Object, 0, len(c.profile.Regions))}
		for _, region := range c.profile.Regions {
			page.Objects = append(page.Objects, provider.Object{ID: region, Name: region, Status: "configured"})
		}
		return page, nil
	}
	if scope.Kind != provider.Instance || !slices.Contains(c.profile.Regions, scope.Region) {
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	if len(cursor) > provider.MaxCursorBytes {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	var page provider.Page
	var err error
	switch c.name {
	case provider.TencentCVM:
		page, err = c.tencentInstances(ctx, scope.Region, cursor)
	case provider.AlibabaECS:
		page, err = c.alibabaInstances(ctx, scope.Region, cursor)
	case provider.HuaweiECS:
		page, err = c.huaweiInstances(ctx, scope.Region, cursor)
	}
	if err != nil {
		return provider.Page{}, err
	}
	if err := page.Validate(cursor); err != nil {
		return provider.Page{}, err
	}
	ids := make(map[string]struct{}, len(page.Objects))
	for _, item := range page.Objects {
		if _, duplicate := ids[item.ID]; duplicate || !instanceID.MatchString(item.ID) || item.Status == "" || strings.ContainsRune(item.Name+item.Status, 0) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		ids[item.ID] = struct{}{}
	}
	return page, nil
}

func (c *Client) request(req *http.Request, dst any) error {
	if err := req.Context().Err(); err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return req.Context().Err()
		}
		return &provider.Failure{Kind: provider.Transient}
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		failure := &provider.Failure{Kind: provider.InvalidResponse}
		switch res.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			failure.Kind = provider.Unauthorized
		case http.StatusTooManyRequests:
			failure.Kind = provider.Throttled
			if delay, err := strconv.ParseInt(res.Header.Get("Retry-After"), 10, 32); err == nil && delay > 0 {
				failure.RetryAfter = time.Duration(delay) * time.Second
			} else if when, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil {
				failure.RetryAfter = max(time.Until(when), 0)
			}
		case http.StatusNotFound:
			failure.Kind = provider.OutcomeUnknown
		case http.StatusBadRequest:
			var response struct {
				Code      string
				ErrorCode string `json:"error_code"`
			}
			if json.NewDecoder(io.LimitReader(res.Body, 16<<10)).Decode(&response) == nil {
				if response.Code == "" {
					response.Code = response.ErrorCode
				}
				return cloudFailure(response.Code)
			}
		default:
			if res.StatusCode >= 500 || res.StatusCode == http.StatusRequestTimeout {
				failure.Kind = provider.Transient
			}
		}
		return failure
	}
	limited := &io.LimitedReader{R: res.Body, N: (8 << 20) + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(dst); err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF || limited.N == 0 {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func cloudFailure(code string) error {
	var kind provider.FailureKind
	switch {
	case code == "InvalidNextToken" || code == "InvalidNextToken.ValueNotSupported" || code == "InvalidParameterValue.NextToken":
		kind = provider.ExpiredCursor
	case strings.HasPrefix(code, "RequestLimitExceeded"), strings.HasPrefix(code, "Throttling"), code == "TooManyRequests":
		kind = provider.Throttled
	case strings.HasPrefix(code, "AuthFailure"), strings.HasPrefix(code, "UnauthorizedOperation"), strings.HasPrefix(code, "Forbidden"),
		code == "InvalidAccessKeyId.NotFound", code == "SignatureDoesNotMatch", code == "InvalidSecurityToken.Expired":
		kind = provider.Unauthorized
	case strings.HasPrefix(code, "InternalError"), code == "ServiceUnavailable":
		kind = provider.Transient
	default:
		kind = provider.InvalidResponse
	}
	return &provider.Failure{Kind: kind}
}

func sha(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func mac(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(data))
	return m.Sum(nil)
}

func tagObject(id, name, status string, tags []*tag) provider.Object {
	object := provider.Object{ID: id, Name: name, Status: status}
	if tags != nil {
		object.Tags = make(map[string]string, len(tags))
		for _, tag := range tags {
			if tag == nil || tag.Key == "" {
				object.Tags = nil
				break
			}
			if value, seen := object.Tags[tag.Key]; seen && value != tag.Value {
				object.Tags = nil
				break
			}
			object.Tags[tag.Key] = tag.Value
		}
	}
	return object
}

type tag struct {
	Key   string
	Value string
}
