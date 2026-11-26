package publiccloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Client) listCOSBuckets(ctx context.Context, query url.Values, result *bucketList) error {
	endpoint := url.URL{Scheme: "https", Host: "service.cos.myqcloud.com", Path: "/", RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	c.signCOSList(req, query, time.Now())
	return c.readBucketXML(req, result)
}

func (c *Client) signCOSList(req *http.Request, query url.Values, now time.Time) {
	names := "max-keys"
	if query.Has("marker") {
		names = "marker;max-keys"
	}
	c.signCOS(req, names, now)
}

func (c *Client) signCOS(req *http.Request, queryNames string, now time.Time) {
	headers := url.Values{"host": {req.URL.Host}}
	headerNames := "host"
	if token := c.profile.SecurityToken; token != "" {
		req.Header.Set("X-Cos-Security-Token", token)
		headers.Set("x-cos-security-token", token)
		headerNames += ";x-cos-security-token"
	}

	window := strconv.FormatInt(now.Unix()-5, 10) + ";" + strconv.FormatInt(now.Unix()+300, 10)
	canonical := strings.ToLower(req.Method) + "\n" + req.URL.Path + "\n" + req.URL.RawQuery + "\n" + headers.Encode() + "\n"
	digest := sha1.Sum([]byte(canonical))
	payload := "sha1\n" + window + "\n" + hex.EncodeToString(digest[:]) + "\n"
	key := hex.EncodeToString(bucketHMACSHA1(c.profile.AccessKeySecret, window))
	signature := hex.EncodeToString(bucketHMACSHA1(key, payload))
	req.Header.Set("Authorization", strings.Join([]string{
		"q-sign-algorithm=sha1", "q-ak=" + c.profile.AccessKeyID, "q-sign-time=" + window, "q-key-time=" + window,
		"q-header-list=" + headerNames, "q-url-param-list=" + queryNames, "q-signature=" + signature,
	}, "&"))
}

// bucketHMACSHA1 用于 COS 和 OBS 的厂商鉴权协议。
func bucketHMACSHA1(secret, message string) []byte {
	digest := hmac.New(sha1.New, []byte(secret))
	_, _ = digest.Write([]byte(message))
	return digest.Sum(nil)
}
