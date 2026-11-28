package publiccloud

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
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
	resourceID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
)

type Config struct {
	Regions                            []string                                      `json:"regions"`
	AccountID                          string                                        `json:"account_id"`
	ProjectID                          string                                        `json:"project_id,omitempty"`
	AccessKeyID                        string                                        `json:"access_key_id,omitempty"`
	AccessKeySecret                    string                                        `json:"access_key_secret,omitempty"`
	SecurityToken                      string                                        `json:"security_token,omitempty"`
	IAMToken                           string                                        `json:"iam_token,omitempty"`
	BucketListAccountWide              bool                                          `json:"bucket_list_account_wide,omitempty"`
	CacheListScopeWide                 bool                                          `json:"cache_list_scope_wide,omitempty"`
	KubernetesListScopeWide            bool                                          `json:"kubernetes_list_scope_wide,omitempty"`
	AllowInstancePower                 bool                                          `json:"allow_instance_power,omitempty"`
	AllowInstanceReboot                bool                                          `json:"allow_instance_reboot,omitempty"`
	AllowInstanceDelete                bool                                          `json:"allow_instance_delete,omitempty"`
	AllowBucketVersioningChanges       bool                                          `json:"allow_bucket_versioning_changes,omitempty"`
	AllowBucketEncryptionEnable        bool                                          `json:"allow_bucket_encryption_enable,omitempty"`
	AllowInstanceRetirement            bool                                          `json:"allow_instance_retirement,omitempty"`
	VolumeCreateMaxGiB                 int                                           `json:"volume_create_max_gib,omitempty"`
	HuaweiVolumeKMSKeyID               string                                        `json:"huawei_volume_kms_key_id,omitempty"`
	AllowVolumeChanges                 bool                                          `json:"allow_volume_changes,omitempty"`
	InstanceResizeSizes                []string                                      `json:"instance_resize_sizes,omitempty"`
	InstanceCreateSizes                []string                                      `json:"instance_create_sizes,omitempty"`
	HuaweiRootVolumeType               string                                        `json:"huawei_root_volume_type,omitempty"`
	HuaweiSnapshotAPI                  string                                        `json:"huawei_snapshot_api,omitempty"`
	AllowSnapshotCreation              bool                                          `json:"allow_snapshot_creation,omitempty"`
	AllowImageCopy                     bool                                          `json:"allow_image_copy,omitempty"`
	HuaweiImageCopyAgency              string                                        `json:"huawei_image_copy_agency,omitempty"`
	AllowImageCreation                 bool                                          `json:"allow_image_creation,omitempty"`
	AllowSecurityGroupDeletion         bool                                          `json:"allow_security_group_deletion,omitempty"`
	AllowVPCDeletion                   bool                                          `json:"allow_vpc_deletion,omitempty"`
	AllowSubnetDeletion                bool                                          `json:"allow_subnet_deletion,omitempty"`
	AllowImageDeletion                 bool                                          `json:"allow_image_deletion,omitempty"`
	AllowSnapshotDeletion              bool                                          `json:"allow_snapshot_deletion,omitempty"`
	AllowSnapshotRollback              bool                                          `json:"allow_snapshot_rollback,omitempty"`
	SnapshotRestoreMaxGiB              int                                           `json:"snapshot_restore_max_gib,omitempty"`
	AllowNetworkCreation               bool                                          `json:"allow_network_creation,omitempty"`
	AllowSecurityGroupCreation         bool                                          `json:"allow_security_group_creation,omitempty"`
	ElasticIPBandwidthLimit            int                                           `json:"elastic_ip_bandwidth_limit,omitempty"`
	AllowElasticIPRelease              bool                                          `json:"allow_elastic_ip_release,omitempty"`
	AllowElasticIPBinding              bool                                          `json:"allow_elastic_ip_binding,omitempty"`
	AllowDNSRecordCreation             bool                                          `json:"allow_dns_record_creation,omitempty"`
	AllowDNSRecordDeletion             bool                                          `json:"allow_dns_record_deletion,omitempty"`
	AllowDNSRecordStatus               bool                                          `json:"allow_dns_record_status,omitempty"`
	AllowDNSRecordUpdate               bool                                          `json:"allow_dns_record_update,omitempty"`
	AllowLoadBalancerBackendAttach     bool                                          `json:"allow_load_balancer_backend_attach,omitempty"`
	AllowLoadBalancerBackendDetach     bool                                          `json:"allow_load_balancer_backend_detach,omitempty"`
	AllowLoadBalancerBackendWeight     bool                                          `json:"allow_load_balancer_backend_weight,omitempty"`
	AllowLoadBalancerTargetGroupRename bool                                          `json:"allow_load_balancer_target_group_rename,omitempty"`
	AllowLoadBalancerListenerRename    bool                                          `json:"allow_load_balancer_listener_rename,omitempty"`
	AllowLoadBalancerRename            bool                                          `json:"allow_load_balancer_rename,omitempty"`
	AllowDatabaseRename                bool                                          `json:"allow_database_rename,omitempty"`
	HuaweiDatabaseRecoveryPlacements   map[string]provider.DatabaseRecoveryPlacement `json:"huawei_database_recovery_placements,omitempty"`
	AlibabaDatabaseRecoveryPlacements  map[string]provider.DatabaseRecoveryPlacement `json:"alibaba_database_recovery_placements,omitempty"`
	DatabaseBackupTimeZones            map[string]string                             `json:"database_backup_time_zones,omitempty"`
	AllowVolumeResize                  bool                                          `json:"allow_volume_resize,omitempty"`
	AllowVolumeRelease                 bool                                          `json:"allow_volume_release,omitempty"`
	AllowCacheRename                   bool                                          `json:"allow_cache_rename,omitempty"`
	HuaweiElasticIPPool                string                                        `json:"huawei_elastic_ip_pool,omitempty"`
	AllowSecurityGroupRules            bool                                          `json:"allow_security_group_rules,omitempty"`
	AllowSecurityRuleRevocation        bool                                          `json:"allow_security_rule_revocation,omitempty"`
}

type Client struct {
	name, identity, projectName string
	profile                     Config
	http                        http.Client
}

func Connect(ctx context.Context, name string, profile Config, transport http.RoundTripper) (*Client, error) {
	if len(profile.Regions) == 0 || len(profile.Regions) > 64 {
		return nil, errors.New("invalid cloud regions")
	}
	if profile.SnapshotRestoreMaxGiB < 0 || profile.SnapshotRestoreMaxGiB > 32768 {
		return nil, errors.New("invalid snapshot recovery size limit")
	}
	if profile.HuaweiSnapshotAPI != "" && (name != provider.HuaweiECS || (profile.HuaweiSnapshotAPI != "v2" && profile.HuaweiSnapshotAPI != "v5")) {
		return nil, errors.New("invalid Huawei snapshot API")
	}
	if profile.HuaweiImageCopyAgency != "" && (name != provider.HuaweiECS || !resourceID.MatchString(profile.HuaweiImageCopyAgency)) {
		return nil, errors.New("invalid Huawei IMS agency")
	}
	profile.Regions = slices.Clone(profile.Regions)
	profile.DatabaseBackupTimeZones = maps.Clone(profile.DatabaseBackupTimeZones)
	for vendor, placements := range map[string]map[string]provider.DatabaseRecoveryPlacement{
		provider.HuaweiECS: profile.HuaweiDatabaseRecoveryPlacements, provider.AlibabaECS: profile.AlibabaDatabaseRecoveryPlacements,
	} {
		if len(placements) > len(profile.Regions) {
			return nil, errors.New("invalid database recovery placements")
		}
		for region, placement := range placements {
			if name != vendor || !slices.Contains(profile.Regions, region) || !placement.ValidFor(vendor) {
				return nil, errors.New("invalid database recovery placement")
			}
		}
	}
	profile.HuaweiDatabaseRecoveryPlacements = maps.Clone(profile.HuaweiDatabaseRecoveryPlacements)
	profile.AlibabaDatabaseRecoveryPlacements = maps.Clone(profile.AlibabaDatabaseRecoveryPlacements)
	profile.InstanceCreateSizes = slices.Clone(profile.InstanceCreateSizes)
	profile.InstanceResizeSizes = slices.Clone(profile.InstanceResizeSizes)
	if len(profile.InstanceResizeSizes) > 128 {
		return nil, errors.New("invalid instance resize sizes")
	}
	for i, size := range profile.InstanceResizeSizes {
		if !provider.ValidInstanceType(size) || slices.Contains(profile.InstanceResizeSizes[:i], size) {
			return nil, errors.New("invalid instance resize sizes")
		}
	}
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
	switch scope.Kind {
	case provider.DNSZone:
		return c.discoverDNSZones(ctx, scope.Region, cursor)
	case provider.ObjectBucket:
		return c.discoverBuckets(ctx, scope, cursor)
	}
	if !slices.Contains(c.profile.Regions, scope.Region) {
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	switch scope.Kind {
	case provider.Instance, provider.Network, provider.Subnet, provider.SecurityGroup, provider.Volume, provider.Snapshot,
		provider.Image, provider.Zone, provider.SecurityGroupRule, provider.ElasticIP, provider.LoadBalancer, provider.DatabaseInstance, provider.CacheInstance,
		provider.KubernetesCluster:
	default:
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	if len(cursor) > provider.MaxCursorBytes || (scope.Kind == provider.Zone && cursor != "") {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	var page provider.Page
	var err error
	switch scope.Kind {
	case provider.DatabaseInstance:
		page, err = c.discoverDatabases(ctx, scope.Region, cursor)
	case provider.CacheInstance:
		page, err = c.discoverCaches(ctx, scope.Region, cursor)
	case provider.KubernetesCluster:
		page, err = c.discoverClusters(ctx, scope.Region, cursor)
	default:
		page, err = c.discoverVendorResources(ctx, scope, cursor)
	}
	if err != nil {
		return provider.Page{}, err
	}
	return validateRegionalPage(scope, cursor, page)
}

func (c *Client) discoverVendorResources(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	var page provider.Page
	var err error
	switch c.name {
	case provider.TencentCVM:
		switch scope.Kind {
		case provider.Instance:
			page, err = c.tencentInstances(ctx, scope.Region, cursor)
		case provider.Volume:
			page, err = c.tencentVolumes(ctx, scope.Region, cursor)
		case provider.Snapshot:
			page, err = c.tencentSnapshots(ctx, scope.Region, cursor, nil)
		case provider.Image:
			page, err = c.tencentImages(ctx, scope.Region, cursor)
		case provider.Zone:
			page, err = c.tencentZones(ctx, scope.Region)
		case provider.SecurityGroupRule:
			page, err = c.tencentSecurityRules(ctx, scope.Region, cursor)
		case provider.ElasticIP:
			page, err = c.tencentElasticIPs(ctx, scope.Region, cursor)
		case provider.LoadBalancer:
			page, err = c.tencentLoadBalancers(ctx, scope.Region, cursor, "")
		default:
			page, err = c.tencentNetworking(ctx, scope, cursor)
		}
	case provider.AlibabaECS:
		switch scope.Kind {
		case provider.Instance:
			page, err = c.alibabaInstances(ctx, scope.Region, cursor)
		case provider.Volume:
			page, err = c.alibabaVolumes(ctx, scope.Region, cursor)
		case provider.Snapshot:
			page, err = c.alibabaSnapshots(ctx, scope.Region, cursor, nil)
		case provider.Image:
			page, err = c.alibabaImages(ctx, scope.Region, cursor)
		case provider.Zone:
			page, err = c.alibabaZones(ctx, scope.Region)
		case provider.SecurityGroupRule:
			page, err = c.alibabaSecurityRules(ctx, scope.Region, cursor)
		case provider.ElasticIP:
			page, err = c.alibabaElasticIPs(ctx, scope.Region, cursor)
		case provider.LoadBalancer:
			page, err = c.alibabaLoadBalancers(ctx, scope.Region, cursor, "")
		default:
			page, err = c.alibabaNetworking(ctx, scope, cursor)
		}
	case provider.HuaweiECS:
		switch scope.Kind {
		case provider.Instance:
			page, err = c.huaweiInstances(ctx, scope.Region, cursor)
		case provider.Volume:
			page, err = c.huaweiVolumes(ctx, scope.Region, cursor)
		case provider.Snapshot:
			page, err = c.huaweiSnapshots(ctx, scope.Region, cursor, nil)
		case provider.Image:
			page, err = c.huaweiImages(ctx, scope.Region, cursor)
		case provider.Zone:
			page, err = c.huaweiZones(ctx, scope.Region)
		case provider.SecurityGroupRule:
			page, err = c.huaweiSecurityRules(ctx, scope.Region, cursor)
		case provider.ElasticIP:
			page, err = c.huaweiElasticIPs(ctx, scope.Region, cursor)
		case provider.LoadBalancer:
			page, err = c.huaweiLoadBalancers(ctx, scope.Region, cursor, "")
		default:
			page, err = c.huaweiNetworking(ctx, scope, cursor)
		}
	default:
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	return page, err
}

func validateRegionalPage(scope provider.Scope, cursor string, page provider.Page) (provider.Page, error) {
	if err := page.Validate(cursor); err != nil {
		return provider.Page{}, err
	}
	ids := make(map[string]struct{}, len(page.Objects))
	for _, item := range page.Objects {
		if _, duplicate := ids[item.ID]; duplicate || !resourceID.MatchString(item.ID) || item.Status == "" || strings.ContainsRune(item.Name, 0) || strings.ContainsRune(item.Status, 0) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if scope.Kind == provider.Snapshot && !item.ValidSnapshot() {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, link := range item.Links {
			if strings.ContainsRune(link.ID, 0) {
				return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
		}
		ids[item.ID] = struct{}{}
	}
	return page, nil
}

func (c *Client) request(req *http.Request, dst any, alsoAccept ...int) error {
	_, err := c.requestStatus(req, dst, alsoAccept...)
	return err
}

func (c *Client) requestStatus(req *http.Request, dst any, alsoAccept ...int) (int, error) {
	if err := req.Context().Err(); err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return 0, req.Context().Err()
		}
		return 0, &provider.Failure{Kind: provider.Transient}
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK && !slices.Contains(alsoAccept, res.StatusCode) {
		return res.StatusCode, cloudHTTPFailure(res)
	}
	if dst == nil && res.StatusCode == http.StatusNoContent {
		return res.StatusCode, nil
	}
	limited := &io.LimitedReader{R: res.Body, N: (8 << 20) + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(dst); err != nil {
		return res.StatusCode, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF || limited.N == 0 {
		return res.StatusCode, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return res.StatusCode, nil
}

func cloudHTTPFailure(res *http.Response) error {
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

func cloudFailure(code string) error {
	var kind provider.FailureKind
	switch {
	case code == "InvalidNextToken" || code == "InvalidNextToken.ValueNotSupported" || code == "InvalidParameterValue.NextToken" || code == "InValidParameter.NextToken":
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
			if tag.Key == launchTag || tag.Key == networkIntentTag || tag.Key == imageMarkerTag {
				continue
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

func pageNumber(cursor string, first int) (int, error) {
	if cursor == "" {
		return first, nil
	}
	number, err := strconv.Atoi(cursor)
	if err != nil || number <= first || number > 1_000_000 || strconv.Itoa(number) != cursor {
		return 0, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	return number, nil
}
