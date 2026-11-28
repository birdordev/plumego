package publiccloud

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareBucketVersioningChange(ctx context.Context, scope provider.Scope, change provider.BucketVersioningChange) (func(context.Context) (string, error), error) {
	if !c.profile.AllowBucketVersioningChanges || !change.Valid() {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	current, err := c.ReadBucketVersioning(ctx, scope, change.ID, change.Location)
	if provider.IsFailure(err, provider.Absent) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if err != nil {
		return nil, err
	}
	if current != change.Current {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	wire := struct {
		XMLName xml.Name `xml:"VersioningConfiguration"`
		Status  string   `xml:"Status"`
	}{Status: "Enabled"}
	if change.Target == provider.BucketVersioningSuspended {
		wire.Status = "Suspended"
	}
	body, err := xml.Marshal(wire)
	if err != nil {
		return nil, err
	}
	name, _, _ := strings.Cut(change.ID, "@")
	return func(ctx context.Context) (string, error) {
		req, err := c.bucketSettingRequest(ctx, http.MethodPut, name, change.Location, "versioning", "PutBucketVersioning", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		if err := c.checkBucketIncarnation(ctx, scope, change.ID, change.Location); err != nil {
			return "", err
		}
		return c.putBucketVersioning(req)
	}, nil
}

func (c *Client) putBucketVersioning(req *http.Request) (string, error) {
	res, err := c.openObjectStorageXML(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return "", cloudHTTPFailure(res)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	if err != nil || len(body) > 4096 || len(bytes.TrimSpace(body)) != 0 {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	header := "X-Cos-Request-Id"
	switch c.name {
	case provider.AlibabaECS:
		header = "X-Oss-Request-Id"
	case provider.HuaweiECS:
		header = "X-Obs-Request-Id"
	}
	if receipt := res.Header.Get(header); receipt != "" {
		return receipt, nil
	}
	return "", &provider.Failure{Kind: provider.OutcomeUnknown}
}
