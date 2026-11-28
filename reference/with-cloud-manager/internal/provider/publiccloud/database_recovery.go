package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

type huaweiRecoveryInstance struct {
	huaweiRDSRow
	Subnet    string   `json:"subnet_id"`
	Group     string   `json:"security_group_id"`
	Flavor    string   `json:"flavor_ref"`
	PublicIPs []string `json:"public_ips"`
	DiskKey   *string  `json:"disk_encryption_id"`
	Volume    struct {
		Type string
		Size int
	}
	Nodes []struct {
		Zone string `json:"availability_zone"`
	}
}

type huaweiRecoveryRequest struct {
	Name    string `json:"name"`
	Region  string `json:"region"`
	VPC     string `json:"vpc_id"`
	Subnet  string `json:"subnet_id"`
	Group   string `json:"security_group_id"`
	Zone    string `json:"availability_zone"`
	Flavor  string `json:"flavor_ref"`
	DiskKey string `json:"disk_encryption_id,omitempty"`
	Volume  struct {
		Type string `json:"type"`
		Size int    `json:"size"`
	} `json:"volume"`
	Point struct {
		Source string `json:"instance_id"`
		Type   string `json:"type"`
		Time   int64  `json:"restore_time"`
	} `json:"restore_point"`
}

func (c *Client) PrepareDatabaseRecovery(ctx context.Context, scope provider.Scope, spec provider.DatabaseRecovery, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if err := c.checkDatabaseRecoveryScope(scope, spec); err != nil {
		return nil, err
	}
	if c.name == provider.AlibabaECS {
		return c.prepareAlibabaDatabaseRecovery(ctx, scope.Region, spec, token)
	}
	if c.name == provider.TencentCVM {
		return c.prepareTencentDatabaseRecovery(ctx, scope, spec, token)
	}
	return c.prepareHuaweiDatabaseRecovery(ctx, scope.Region, spec)
}

func (c *Client) prepareHuaweiDatabaseRecovery(ctx context.Context, region string, spec provider.DatabaseRecovery) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if allowed, ok := c.profile.HuaweiDatabaseRecoveryPlacements[region]; !ok || allowed != spec.DatabaseRecoveryPlacement {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	source, err := c.readHuaweiRecoveryInstance(ctx, region, spec.SourceID)
	if err != nil {
		return nil, err
	}
	if source.Status != "ACTIVE" || !strings.EqualFold(source.Datastore.Type, "mysql") || source.Name == spec.Name ||
		source.Volume.Size < 1 || source.Volume.Size > spec.StorageGiB || source.Network == spec.VPC ||
		!huaweiResourceID.MatchString(source.Network) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	windows, err := c.huaweiRestoreWindows(ctx, region, spec.SourceID)
	if err != nil {
		return nil, err
	}
	if err := provider.NormalizeDatabaseRestoreWindows(windows); err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(windows, func(window provider.DatabaseRestoreWindow) bool { return window.Contains(spec.RestoreTime) }) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	if err := c.checkHuaweiRecoveryFlavor(ctx, region, source.Datastore.Version, spec.DatabaseRecoveryPlacement); err != nil {
		return nil, err
	}
	payload := huaweiRecoveryRequest{
		Name: spec.Name, Region: region, VPC: spec.VPC, Subnet: spec.Subnet,
		Group: spec.SecurityGroup, Zone: spec.Zone, Flavor: spec.Flavor, DiskKey: *source.DiskKey,
	}
	payload.Volume.Type, payload.Volume.Size = spec.StorageType, spec.StorageGiB
	payload.Point.Source, payload.Point.Type, payload.Point.Time = spec.SourceID, "timestamp", spec.RestoreTime.UnixMilli()
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		return c.createHuaweiRecoveryInstance(ctx, region, spec, body)
	}, nil
}

func (c *Client) checkHuaweiRecoveryFlavor(ctx context.Context, region, version string, placement provider.DatabaseRecoveryPlacement) error {
	if version == "" || len(version) > 96 {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	endpoint := c.huaweiRecoveryURL(region, "/flavors/mysql", url.Values{"version_name": {version}, "spec_code": {placement.Flavor}})
	var result struct {
		Flavors []struct {
			Code string            `json:"spec_code"`
			Mode string            `json:"instance_mode"`
			AZ   map[string]string `json:"az_status"`
		}
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return err
	}
	if len(result.Flavors) != 1 || result.Flavors[0].Code != placement.Flavor ||
		result.Flavors[0].Mode != "single" || result.Flavors[0].AZ[placement.Zone] != "normal" {
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	return nil
}

func (c *Client) checkDatabaseRecoveryScope(scope provider.Scope, spec provider.DatabaseRecovery) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.DatabaseInstance || !slices.Contains(c.profile.Regions, scope.Region) || !spec.ValidFor(c.name) {
		return &provider.Failure{Kind: provider.Unsupported}
	}
	return nil
}

func (c *Client) huaweiRecoveryURL(region, path string, query url.Values) url.URL {
	return url.URL{Scheme: "https", Host: "rds." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + path, RawQuery: query.Encode()}
}

func (c *Client) createHuaweiRecoveryInstance(ctx context.Context, region string, spec provider.DatabaseRecovery, body []byte) (provider.LaunchReceipt, error) {
	endpoint := c.huaweiRecoveryURL(region, "/instances", nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	req.GetBody = nil
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.Header.Set("Content-Type", "application/json")
	var result struct {
		JobID    string `json:"job_id"`
		OrderID  string `json:"order_id"`
		Instance struct{ ID, Name string }
	}
	status, err := c.requestStatus(req, &result, http.StatusAccepted)
	if err != nil || status != http.StatusAccepted || !resourceID.MatchString(result.JobID) ||
		!provider.ValidDatabaseRecoveryInstanceID(c.name, result.Instance.ID) || result.Instance.ID == spec.SourceID ||
		result.Instance.Name != spec.Name || result.OrderID != "" {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return provider.LaunchReceipt{RequestID: result.JobID, ResourceID: result.Instance.ID}, nil
}

func (c *Client) ObserveDatabaseRecovery(ctx context.Context, scope provider.Scope, spec provider.DatabaseRecovery, token string, receipt provider.LaunchReceipt) (provider.Object, error) {
	if err := c.checkDatabaseRecoveryScope(scope, spec); err != nil {
		return provider.Object{}, err
	}
	if c.name == provider.TencentCVM {
		return c.observeTencentDatabaseRecovery(ctx, scope, spec, token, receipt)
	}
	if !resourceID.MatchString(receipt.RequestID) || !provider.ValidDatabaseRecoveryInstanceID(c.name, receipt.ResourceID) || receipt.ResourceID == spec.SourceID {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if c.name == provider.AlibabaECS {
		return c.observeAlibabaDatabaseRecovery(ctx, scope.Region, spec, receipt.ResourceID)
	}
	return c.observeHuaweiDatabaseRecovery(ctx, scope.Region, spec, receipt)
}

func (c *Client) observeHuaweiDatabaseRecovery(ctx context.Context, region string, spec provider.DatabaseRecovery, receipt provider.LaunchReceipt) (provider.Object, error) {
	endpoint := c.huaweiRecoveryURL(region, "/jobs", url.Values{"id": {receipt.RequestID}})
	var result struct {
		Job struct {
			ID, Status string
			Instance   struct{ ID, Name string }
		}
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return provider.Object{}, err
	}
	if result.Job.ID != receipt.RequestID || result.Job.Instance.ID != receipt.ResourceID || result.Job.Instance.Name != spec.Name {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	switch result.Job.Status {
	case "Running":
		return provider.Object{}, nil
	case "Completed":
	default:
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	target, err := c.readHuaweiRecoveryInstance(ctx, region, receipt.ResourceID)
	if err != nil {
		return provider.Object{}, err
	}
	if target.Status == "BUILD" {
		return provider.Object{}, nil
	}
	source, err := c.readHuaweiRecoveryInstance(ctx, region, spec.SourceID)
	if err != nil {
		return provider.Object{}, err
	}
	if target.Status != "ACTIVE" || target.Name != spec.Name || target.Network != spec.VPC || target.Subnet != spec.Subnet ||
		target.Group != spec.SecurityGroup || target.Flavor != spec.Flavor || target.Volume.Type != spec.StorageType ||
		target.Volume.Size != spec.StorageGiB || target.PublicIPs == nil || len(target.PublicIPs) != 0 ||
		len(target.Nodes) != 1 || target.Nodes[0].Zone != spec.Zone || *target.DiskKey != *source.DiskKey ||
		!strings.EqualFold(target.Datastore.Type, "mysql") {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return databaseObject(target.ID, target.Name, target.Status, provider.HuaweiRDS, target.Datastore.Type, target.Datastore.Version, target.Network)
}

func (c *Client) readHuaweiRecoveryInstance(ctx context.Context, region, id string) (huaweiRecoveryInstance, error) {
	endpoint := c.huaweiRecoveryURL(region, "/instances", url.Values{"id": {id}, "limit": {"50"}, "offset": {"0"}})
	var result struct {
		Instances []huaweiRecoveryInstance
		Total     *int `json:"total_count"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return huaweiRecoveryInstance{}, err
	}
	if result.Total != nil && *result.Total == 0 && result.Instances != nil && len(result.Instances) == 0 {
		return huaweiRecoveryInstance{}, &provider.Failure{Kind: provider.Absent}
	}
	if result.Total == nil || *result.Total != 1 || len(result.Instances) != 1 {
		return huaweiRecoveryInstance{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	instance := result.Instances[0]
	if instance.ID != id || instance.Region != region || (instance.Project != "" && instance.Project != c.profile.ProjectID) ||
		instance.DiskKey == nil || (*instance.DiskKey != "" && !huaweiResourceID.MatchString(*instance.DiskKey)) {
		return huaweiRecoveryInstance{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return instance, nil
}
