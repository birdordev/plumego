package publiccloud

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"

	"with-cloud-manager/internal/provider"
)

type huaweiLaunchSubnet struct {
	ID, Status string
	Project    string `json:"tenant_id"`
	VPC        string `json:"vpc_id"`
	Zone       string `json:"availability_zone"`
	Network    string `json:"neutron_network_id"`
	IPv4Subnet string `json:"neutron_subnet_id"`
}

func (c *Client) prepareHuaweiCreation(ctx context.Context, region string, spec provider.InstanceSpec, token, marker string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	switch c.profile.HuaweiRootVolumeType {
	case "SAS", "GPSSD", "SSD", "ESSD":
	default:
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	subnet, err := c.readHuaweiLaunchSubnet(ctx, region, spec.NetworkID)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		return c.createHuaweiInstance(ctx, region, spec, token, marker, subnet)
	}, nil
}

func (c *Client) readHuaweiLaunchSubnet(ctx context.Context, region, id string) (huaweiLaunchSubnet, error) {
	endpoint := url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/subnets/" + id}
	var response struct{ Subnet huaweiLaunchSubnet }
	if err := c.readHuawei(ctx, endpoint, &response); err != nil {
		return huaweiLaunchSubnet{}, err
	}
	subnet := response.Subnet
	if subnet.Project != c.profile.ProjectID {
		return huaweiLaunchSubnet{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if subnet.ID != id || subnet.Status != "ACTIVE" ||
		!huaweiResourceID.MatchString(subnet.VPC) || !huaweiResourceID.MatchString(subnet.Network) ||
		!huaweiResourceID.MatchString(subnet.IPv4Subnet) || subnet.Network == subnet.IPv4Subnet ||
		(subnet.Zone != "" && !resourceID.MatchString(subnet.Zone)) {
		return huaweiLaunchSubnet{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return subnet, nil
}

func huaweiLaunchToken(token string) string {
	digest := sha256.Sum256([]byte("huaweicloud-ecs-client-token\x00" + token))
	digest[6] = digest[6]&0x0f | 0x40
	digest[8] = digest[8]&0x3f | 0x80
	value := hex.EncodeToString(digest[:16])
	return value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:]
}

func (c *Client) createHuaweiInstance(ctx context.Context, region string, spec provider.InstanceSpec, token, marker string, subnet huaweiLaunchSubnet) (provider.LaunchReceipt, error) {
	type id struct {
		ID string `json:"id"`
	}
	type nic struct {
		Subnet string `json:"subnet_id"`
	}
	type serverTag struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	type rootVolume struct {
		Type string `json:"volumetype"`
	}
	type extendParams struct {
		ChargingMode string `json:"chargingMode"`
	}
	type server struct {
		Name           string       `json:"name"`
		Image          string       `json:"imageRef"`
		Flavor         string       `json:"flavorRef"`
		VPC            string       `json:"vpcid"`
		Zone           string       `json:"availability_zone,omitempty"`
		Count          int          `json:"count"`
		Root           rootVolume   `json:"root_volume"`
		NICs           []nic        `json:"nics"`
		SecurityGroups []id         `json:"security_groups"`
		Tags           []serverTag  `json:"server_tags"`
		Extended       extendParams `json:"extendparam"`
	}
	input := struct {
		Server server `json:"server"`
	}{Server: server{
		Name: spec.Name, Image: spec.ImageID, Flavor: spec.Size, VPC: subnet.VPC, Zone: subnet.Zone, Count: 1,
		Root: rootVolume{Type: c.profile.HuaweiRootVolumeType}, NICs: []nic{{Subnet: subnet.Network}},
		SecurityGroups: []id{{ID: spec.SecurityGroup}}, Tags: []serverTag{{Key: launchTag, Value: marker}},
		Extended: extendParams{ChargingMode: "postPaid"},
	}}
	body, err := json.Marshal(input)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	endpoint := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1.1/" + c.profile.ProjectID + "/cloudservers"}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.Header.Set("X-Client-Token", huaweiLaunchToken(token))
	var result struct {
		JobID string   `json:"job_id"`
		IDs   []string `json:"serverIds"`
	}
	if err := c.request(req, &result, http.StatusCreated, http.StatusAccepted); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if !resourceID.MatchString(result.JobID) || len(result.IDs) > 1 ||
		(len(result.IDs) == 1 && !huaweiResourceID.MatchString(result.IDs[0])) {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	receipt := provider.LaunchReceipt{RequestID: result.JobID}
	if len(result.IDs) == 1 {
		receipt.ResourceID = result.IDs[0]
	}
	return receipt, nil
}

func (c huaweiOperator) FindCreatedInstanceWithReceipt(ctx context.Context, scope provider.Scope, spec provider.InstanceSpec, token string, receipt provider.LaunchReceipt) (provider.Object, error) {
	if err := c.creationScope(scope, spec, token); err != nil {
		return provider.Object{}, err
	}
	return c.findHuaweiCreation(ctx, scope.Region, spec, launchMarker(scope, token), receipt)
}

func (c *Client) findHuaweiCreation(ctx context.Context, region string, spec provider.InstanceSpec, marker string, receipt provider.LaunchReceipt) (provider.Object, error) {
	instanceID := receipt.ResourceID
	var err error
	if receipt.RequestID != "" {
		instanceID, err = c.huaweiCreationTarget(ctx, region, receipt)
	} else if instanceID == "" {
		instanceID, err = c.findHuaweiTaggedInstance(ctx, region, marker)
	}
	if err != nil {
		return provider.Object{}, err
	}
	if !huaweiResourceID.MatchString(instanceID) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	row, err := c.describeHuaweiInstance(ctx, region, instanceID)
	if err != nil {
		return provider.Object{}, err
	}
	if row.Status == "BUILD" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if row.Status != "ACTIVE" || row.Name != spec.Name || row.Image.ID != spec.ImageID || row.Flavor.ID != spec.Size ||
		row.Metadata.ChargingMode != "0" || len(row.SecurityGroups) != 1 || row.SecurityGroups[0].ID != spec.SecurityGroup {
		return provider.Object{}, invalid
	}
	endpoint := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/cloudservers/" + instanceID + "/tags"}
	var response struct{ Tags []*tag }
	if err := c.readHuawei(ctx, endpoint, &response); err != nil {
		return provider.Object{}, err
	}
	if !matchingLaunchTag(response.Tags, marker) {
		return provider.Object{}, invalid
	}
	subnet, err := c.readHuaweiLaunchSubnet(ctx, region, spec.NetworkID)
	if err != nil {
		return provider.Object{}, err
	}
	if row.Metadata.VPC != subnet.VPC || (subnet.Zone != "" && row.Zone != subnet.Zone) {
		return provider.Object{}, invalid
	}
	if err := c.verifyHuaweiLaunchInterface(ctx, region, instanceID, subnet); err != nil {
		return provider.Object{}, err
	}
	object := tagObject(row.ID, row.Name, row.Status, response.Tags)
	object.InstanceType = row.Flavor.ID
	object.Link(provider.Zone, row.Zone, "in_zone")
	object.Link(provider.Network, subnet.VPC, "in_network")
	object.Link(provider.Subnet, subnet.ID, "in_subnet")
	object.Link(provider.Image, spec.ImageID, "uses_image")
	object.Link(provider.SecurityGroup, spec.SecurityGroup, "uses_security_group")
	for _, volume := range row.Volumes {
		object.Link(provider.Volume, volume.ID, "uses_volume")
	}
	return object, nil
}

func (c *Client) huaweiCreationTarget(ctx context.Context, region string, receipt provider.LaunchReceipt) (string, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if !resourceID.MatchString(receipt.RequestID) {
		return "", invalid
	}
	job, err := c.readHuaweiJob(ctx, region, receipt.RequestID)
	if err != nil {
		return "", err
	}
	if job.ID != receipt.RequestID || (job.Type != "createServer" && job.Type != "createSingleServer") {
		return "", invalid
	}
	if job.Status == "INIT" || job.Status == "RUNNING" {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	id := job.Entities.ServerID
	if id == "" && len(job.Entities.Subjobs) == 1 {
		id = job.Entities.Subjobs[0].Entities.ServerID
	}
	if !huaweiResourceID.MatchString(id) || (receipt.ResourceID != "" && receipt.ResourceID != id) {
		return "", invalid
	}
	status, err := job.result(id, receipt.RequestID, job.Type, "createSingleServer")
	if err != nil || status != provider.ActionSucceeded {
		return "", invalid
	}
	return id, nil
}

func (c *Client) findHuaweiTaggedInstance(ctx context.Context, region, marker string) (string, error) {
	query := url.Values{"limit": {"2"}, "tags": {launchTag + "=" + marker}}
	endpoint := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1.1/" + c.profile.ProjectID + "/cloudservers/detail", RawQuery: query.Encode()}
	var result struct {
		Links   []struct{ Rel string } `json:"servers_links"`
		Servers []struct {
			ID      string
			Project string `json:"tenant_id"`
		}
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return "", err
	}
	if result.Servers == nil || len(result.Servers) > 1 {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	for _, link := range result.Links {
		if link.Rel == "next" {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	if len(result.Servers) == 0 {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	if result.Servers[0].Project != c.profile.ProjectID {
		return "", &provider.Failure{Kind: provider.Unauthorized}
	}
	return result.Servers[0].ID, nil
}

func (c *Client) verifyHuaweiLaunchInterface(ctx context.Context, region, id string, subnet huaweiLaunchSubnet) error {
	endpoint := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/cloudservers/" + id + "/os-interface"}
	var result struct {
		Interfaces []struct {
			Network string `json:"net_id"`
			Port    string `json:"port_id"`
			State   string `json:"port_state"`
			IPs     []struct {
				Subnet string `json:"subnet_id"`
				IP     string `json:"ip_address"`
			} `json:"fixed_ips"`
		} `json:"interfaceAttachments"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if len(result.Interfaces) != 1 {
		return invalid
	}
	nic := result.Interfaces[0]
	if nic.Network != subnet.Network || !huaweiResourceID.MatchString(nic.Port) || len(nic.IPs) != 1 || nic.IPs[0].Subnet != subnet.IPv4Subnet {
		return invalid
	}
	if nic.State == "BUILD" {
		return &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	ip, err := netip.ParseAddr(nic.IPs[0].IP)
	if err != nil || !ip.Is4() || nic.State != "ACTIVE" {
		return invalid
	}
	return nil
}
