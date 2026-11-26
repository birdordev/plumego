package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) renameHuaweiDatabase(ctx context.Context, region string, change provider.DatabaseRename) (string, error) {
	endpoint := url.URL{Scheme: "https", Host: "rds." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/instances/" + change.ID + "/name"}
	return c.putHuaweiResourceName(ctx, endpoint, change.Name, http.StatusOK)
}

func (c *Client) putHuaweiResourceName(ctx context.Context, endpoint url.URL, name string, expectedStatus int) (string, error) {
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.GetBody = nil
	res, err := c.http.Do(req)
	if err != nil {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != expectedStatus {
		return "", cloudHTTPFailure(res)
	}
	response, err := io.ReadAll(io.LimitReader(res.Body, 1025))
	if err != nil || (len(bytes.TrimSpace(response)) != 0 && !bytes.Equal(bytes.TrimSpace(response), []byte("{}"))) || len(response) > 1024 {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	receipt := res.Header.Get("X-Request-Id")
	if resourceID.MatchString(receipt) {
		return receipt, nil
	}
	return "", nil
}
