package publiccloud

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

type obsCredential struct {
	Access, Secret string
	Token          string    `json:"securitytoken"`
	Expires        time.Time `json:"expires_at"`
}

// OBS 临时凭据只包含当前请求所需的动作和资源。IAM Token 只发送给 IAM。
func (c *Client) obsScopedCredential(ctx context.Context, action, bucket string) (obsCredential, error) {
	body := `{"auth":{"identity":{"methods":["token"],"token":{"duration_seconds":900},"policy":{"Version":"1.1","Statement":[{"Effect":"Allow","Action":["obs:bucket:` +
		action + `"],"Resource":["OBS:*:*:bucket:` + bucket + `"]}]}}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://iam.myhuaweicloud.com/v3.0/OS-CREDENTIAL/securitytokens", strings.NewReader(body))
	if err != nil {
		return obsCredential{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var result struct{ Credential obsCredential }
	if err := c.request(req, &result, http.StatusCreated); err != nil {
		return obsCredential{}, err
	}
	key := result.Credential
	now := time.Now()
	if !credential(key.Access, 128, false) || !credential(key.Secret, 256, false) || !credential(key.Token, 8192, false) ||
		!key.Expires.After(now) || key.Expires.After(now.Add(16*time.Minute)) {
		return obsCredential{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return key, nil
}

func (c *Client) listOBSBuckets(ctx context.Context, result *bucketList) error {
	key, err := c.obsScopedCredential(ctx, "ListAllMyBuckets", "*")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://obs."+c.profile.Regions[0]+".myhuaweicloud.com/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Obs-Bucket-Type", "OBJECT")
	signOBS(req, key, "/", time.Now().UTC())
	return c.readBucketXML(req, result)
}

func signOBS(req *http.Request, key obsCredential, resource string, now time.Time) {
	date := now.Format(http.TimeFormat)
	req.Header.Set("Date", date)
	var headers string
	if bucketType := req.Header.Get("X-Obs-Bucket-Type"); bucketType != "" {
		headers = "x-obs-bucket-type:" + bucketType + "\n"
	}
	if key.Token != "" {
		req.Header.Set("X-Obs-Security-Token", key.Token)
		headers += "x-obs-security-token:" + key.Token + "\n"
	}
	payload := req.Method + "\n\n\n" + date + "\n" + headers + resource
	signature := base64.StdEncoding.EncodeToString(bucketHMACSHA1(key.Secret, payload))
	req.Header.Set("Authorization", "OBS "+key.Access+":"+signature)
}
