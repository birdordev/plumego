package awsec2

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"with-cloud-manager/internal/provider"
)

const Name = "aws-ec2"

var accountID = regexp.MustCompile(`^[0-9]{12}$`)
var regionName = regexp.MustCompile(`^[a-z]{2}(-[a-z0-9]+)+-[0-9]+$`)

type Config struct {
	Region                      string   `json:"region"`
	RoleARN                     string   `json:"role_arn"`
	ExternalID                  string   `json:"external_id"`
	AllowInstanceDelete         bool     `json:"allow_instance_delete,omitempty"`
	AllowVolumeChanges          bool     `json:"allow_volume_changes,omitempty"`
	AllowNetworkCreation        bool     `json:"allow_network_creation,omitempty"`
	AllowSecurityGroupCreation  bool     `json:"allow_security_group_creation,omitempty"`
	AllowSecurityGroupRules     bool     `json:"allow_security_group_rules,omitempty"`
	AllowSecurityRuleRevocation bool     `json:"allow_security_rule_revocation,omitempty"`
	InstanceCreateSizes         []string `json:"instance_create_sizes,omitempty"`
	SerialConsoleHost           string   `json:"serial_console_host,omitempty"`
}

type Client struct {
	ec2                         *ec2.Client
	identity                    string
	account                     string
	region                      string
	allowInstanceDelete         bool
	allowVolumeChanges          bool
	allowNetworkCreation        bool
	allowSecurityGroupCreation  bool
	allowSecurityGroupRules     bool
	allowSecurityRuleRevocation bool
	instanceCreateSizes         []string
	serial                      *serialConsole
}

func (c *Client) Identity() string { return c.identity }

func Connect(ctx context.Context, profile Config) (*Client, error) {
	role, err := arn.Parse(profile.RoleARN)
	if err != nil || !accountID.MatchString(role.AccountID) || role.Service != "iam" ||
		!strings.HasPrefix(role.Resource, "role/") || role.Region != "" ||
		!regionName.MatchString(profile.Region) || profile.ExternalID == "" {
		return nil, errors.New("invalid AWS role profile")
	}
	transport := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(profile.Region),
		config.WithHTTPClient(transport))
	if err != nil {
		return nil, errors.New("cannot load AWS workload credentials")
	}
	// 盘点客户端使用 SDK 的分区端点规则，丢弃本机服务地址覆盖设置。
	return connectWithConfig(ctx, profile, role, aws.Config{
		Region: profile.Region, Credentials: cfg.Credentials, HTTPClient: transport,
		RetryMaxAttempts: 2,
	})
}

func connectWithConfig(ctx context.Context, profile Config, role arn.ARN, cfg aws.Config) (*Client, error) {
	serialEndpoint, err := serialAPIEndpoint(role.Partition, profile.Region, profile.SerialConsoleHost)
	if err != nil {
		return nil, err
	}
	sessionName := "cloud-manager-inventory"
	if serialEndpoint != "" {
		sessionName = "cloud-manager-serial-console"
	}
	cfg.Credentials = aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(sts.NewFromConfig(cfg), profile.RoleARN,
		func(options *stscreds.AssumeRoleOptions) {
			options.ExternalID = aws.String(profile.ExternalID)
			options.RoleSessionName = sessionName
		}))
	identity, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, translate(ctx, err)
	}
	caller, err := arn.Parse(aws.ToString(identity.Arn))
	roleName := role.Resource[strings.LastIndex(role.Resource, "/")+1:]
	if err != nil || aws.ToString(identity.Account) != role.AccountID ||
		caller.AccountID != role.AccountID || caller.Partition != role.Partition ||
		caller.Service != "sts" || !strings.HasPrefix(caller.Resource, "assumed-role/"+roleName+"/") {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	client := &Client{ec2: ec2.NewFromConfig(cfg), identity: role.Partition + ":" + role.AccountID,
		account: role.AccountID, region: profile.Region, allowInstanceDelete: profile.AllowInstanceDelete,
		allowVolumeChanges:          profile.AllowVolumeChanges,
		allowNetworkCreation:        profile.AllowNetworkCreation,
		allowSecurityGroupCreation:  profile.AllowSecurityGroupCreation,
		allowSecurityGroupRules:     profile.AllowSecurityGroupRules,
		allowSecurityRuleRevocation: profile.AllowSecurityRuleRevocation,
		instanceCreateSizes:         append([]string(nil), profile.InstanceCreateSizes...)}
	if serialEndpoint != "" {
		client.serial = &serialConsole{endpoint: serialEndpoint, host: profile.SerialConsoleHost, region: profile.Region,
			credentials: cfg.Credentials, http: cfg.HTTPClient}
	}
	return client, nil
}

func translate(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		switch api.ErrorCode() {
		case "AccessDenied", "AccessDeniedException", "UnauthorizedOperation", "AuthFailure", "InvalidClientTokenId", "ExpiredToken":
			return &provider.Failure{Kind: provider.Unauthorized}
		case "RequestLimitExceeded", "Throttling", "ThrottlingException", "TooManyRequestsException":
			return &provider.Failure{Kind: provider.Throttled}
		case "InvalidNextToken", "InvalidPaginationToken":
			return &provider.Failure{Kind: provider.ExpiredCursor}
		}
		if strings.HasSuffix(api.ErrorCode(), ".NotFound") {
			return &provider.Failure{Kind: provider.OutcomeUnknown}
		}
		if api.ErrorFault() == smithy.FaultServer {
			return &provider.Failure{Kind: provider.Transient}
		}
	}
	var response *smithyhttp.ResponseError
	if errors.As(err, &response) {
		switch response.HTTPStatusCode() {
		case http.StatusTooManyRequests:
			return &provider.Failure{Kind: provider.Throttled}
		case http.StatusUnauthorized, http.StatusForbidden:
			return &provider.Failure{Kind: provider.Unauthorized}
		case http.StatusNotFound:
			return &provider.Failure{Kind: provider.OutcomeUnknown}
		}
		if response.HTTPStatusCode() >= http.StatusInternalServerError {
			return &provider.Failure{Kind: provider.Transient}
		}
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return &provider.Failure{Kind: provider.Transient}
}
