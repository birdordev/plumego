package publiccloud

import (
	"context"
	"encoding/xml"
	"net/http"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareBucketVersioningChange(ctx context.Context, scope provider.Scope, change provider.BucketVersioningChange) (func(context.Context) (string, error), error) {
	if !c.profile.AllowBucketVersioningChanges || !change.Valid() {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	status := "Enabled"
	if change.Target == provider.BucketVersioningSuspended {
		status = "Suspended"
	}
	body, err := xml.Marshal(struct {
		XMLName xml.Name `xml:"VersioningConfiguration"`
		Status  string   `xml:"Status"`
	}{Status: status})
	if err != nil {
		return nil, err
	}
	return prepareBucketSetting(ctx, c, scope, change.ID, change.Location, change.Current, c.ReadBucketVersioning,
		"versioning", "PutBucketVersioning", body, http.StatusOK)
}
