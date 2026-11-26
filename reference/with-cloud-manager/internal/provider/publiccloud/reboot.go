package publiccloud

import (
	"context"
	"net/url"

	"with-cloud-manager/internal/provider"
)

type rebootObserver struct{ *Client }

func (c *Client) InstanceOperator() provider.InstanceOperator {
	switch c.name {
	case provider.TencentCVM:
		return rebootObserver{c}
	case provider.HuaweiECS:
		return huaweiOperator{rebootObserver{c}}
	default:
		return c
	}
}

func (c rebootObserver) ObserveReboot(ctx context.Context, scope provider.Scope, id, receipt string) (provider.ActionResult, error) {
	if err := c.checkInstanceScope(scope, id); err != nil {
		return "", err
	}
	if !resourceID.MatchString(receipt) {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	switch c.name {
	case provider.TencentCVM:
		return c.observeTencentReboot(ctx, scope.Region, id, receipt)
	case provider.HuaweiECS:
		return c.observeHuaweiJob(ctx, scope.Region, id, receipt, "", "")
	default:
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
}

func (c *Client) observeTencentReboot(ctx context.Context, region, id, receipt string) (provider.ActionResult, error) {
	row, err := c.describeTencentInstance(ctx, region, id)
	if err != nil {
		return "", err
	}
	if row.LatestOperationRequestId != receipt {
		return provider.ActionPending, nil
	}
	if row.LatestOperation != "RebootInstances" {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	switch row.LatestOperationState {
	case "OPERATING":
		return provider.ActionPending, nil
	case "SUCCESS":
		return provider.ActionSucceeded, nil
	case "FAILED":
		return provider.ActionFailed, nil
	default:
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
}

type huaweiInstanceJob struct {
	ID        string `json:"job_id"`
	Type      string `json:"job_type"`
	ProjectID string `json:"project_id"`
	Status    string
	Entities  struct {
		ServerID    string `json:"server_id"`
		SubjobCount *int   `json:"sub_jobs_total"`
		Subjobs     []struct {
			Status   string
			Type     string `json:"job_type"`
			Entities struct {
				ServerID string `json:"server_id"`
			}
		} `json:"sub_jobs"`
	}
}

func (c *Client) observeHuaweiJob(ctx context.Context, region, id, receipt, jobType, childType string) (provider.ActionResult, error) {
	job, err := c.readHuaweiJob(ctx, region, receipt)
	if err != nil {
		return "", err
	}
	return job.result(id, receipt, jobType, childType)
}

func (c *Client) readHuaweiJob(ctx context.Context, region, receipt string) (huaweiInstanceJob, error) {
	u := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/jobs/" + receipt}
	var job huaweiInstanceJob
	if err := c.readHuawei(ctx, u, &job); err != nil {
		return huaweiInstanceJob{}, err
	}
	if job.ProjectID != "" && job.ProjectID != c.profile.ProjectID {
		return huaweiInstanceJob{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	return job, nil
}

func (job huaweiInstanceJob) result(id, receipt, jobType, childType string) (provider.ActionResult, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if job.ID != receipt || (jobType != "" && job.Type != jobType) {
		return "", invalid
	}
	entities := job.Entities
	if entities.ServerID != "" && entities.ServerID != id {
		return "", invalid
	}
	if len(entities.Subjobs) > 1 || (entities.SubjobCount != nil &&
		(*entities.SubjobCount < len(entities.Subjobs) || *entities.SubjobCount > 1)) {
		return "", invalid
	}
	targetConfirmed := entities.ServerID == id
	childStatus := ""
	if len(entities.Subjobs) == 1 {
		child := entities.Subjobs[0]
		if (child.Entities.ServerID != "" && child.Entities.ServerID != id) ||
			(childType != "" && child.Type != childType) {
			return "", invalid
		}
		targetConfirmed = targetConfirmed || child.Entities.ServerID == id
		childStatus = child.Status
	}
	switch job.Status {
	case "INIT", "RUNNING":
		return provider.ActionPending, nil
	case "SUCCESS", "FAIL":
		if !targetConfirmed || (entities.SubjobCount != nil && *entities.SubjobCount != len(entities.Subjobs)) ||
			(len(entities.Subjobs) == 1 && childStatus != job.Status) {
			return "", invalid
		}
		if job.Status == "SUCCESS" {
			return provider.ActionSucceeded, nil
		}
		return provider.ActionFailed, nil
	default:
		return "", invalid
	}
}
