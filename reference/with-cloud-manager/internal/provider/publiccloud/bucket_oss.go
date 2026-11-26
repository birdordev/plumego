package publiccloud

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/url"
	"time"
)

func (c *Client) listOSSBuckets(ctx context.Context, query url.Values, result *bucketList) error {
	region := c.profile.Regions[0]
	endpoint := url.URL{Scheme: "https", Host: "oss-" + region + ".aliyuncs.com", Path: "/", RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	c.signOSS(req, region, "/", time.Now().UTC())
	return c.readBucketXML(req, result)
}

func (c *Client) signOSS(req *http.Request, region, canonicalPath string, now time.Time) {
	const algorithm = "OSS4-HMAC-SHA256"
	date, stamp := now.Format("20060102"), now.Format("20060102T150405Z")
	scope := date + "/" + region + "/oss/aliyun_v4_request"
	req.Header.Set("X-Oss-Date", stamp)
	req.Header.Set("X-Oss-Content-Sha256", "UNSIGNED-PAYLOAD")
	headers := "host:" + req.URL.Host + "\nx-oss-content-sha256:UNSIGNED-PAYLOAD\nx-oss-date:" + stamp + "\n"
	if token := c.profile.SecurityToken; token != "" {
		req.Header.Set("X-Oss-Security-Token", token)
		headers += "x-oss-security-token:" + token + "\n"
	}
	canonical := req.Method + "\n" + canonicalPath + "\n" + req.URL.RawQuery + "\n" + headers + "\nhost\nUNSIGNED-PAYLOAD"
	payload := algorithm + "\n" + stamp + "\n" + scope + "\n" + sha([]byte(canonical))
	key := mac(mac(mac(mac([]byte("aliyun_v4"+c.profile.AccessKeySecret), date), region), "oss"), "aliyun_v4_request")
	signature := hex.EncodeToString(mac(key, payload))
	req.Header.Set("Authorization", algorithm+" Credential="+c.profile.AccessKeyID+"/"+scope+",AdditionalHeaders=host,Signature="+signature)
}
