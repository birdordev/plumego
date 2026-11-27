package publiccloud

import (
	"context"

	"with-cloud-manager/internal/provider"
)

func (c *Client) observeHuaweiVolumeJob(ctx context.Context, region, receipt, jobType, volumeID string, expectedSize int) (provider.ActionResult, error) {
	if !resourceID.MatchString(receipt) {
		return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
	}
	endpoint := huaweiEVS(region, "/v1/"+c.profile.ProjectID+"/jobs/"+receipt)
	var response struct {
		JobID    string `json:"job_id"`
		JobType  string `json:"job_type"`
		Status   string
		Entities struct {
			ID   string `json:"volume_id"`
			Size *int
		}
	}
	if err := c.readHuawei(ctx, endpoint, &response); err != nil {
		return provider.ActionPending, err
	}
	if response.JobID != receipt || response.JobType != jobType ||
		(response.Entities.ID != "" && response.Entities.ID != volumeID) {
		return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
	}
	switch response.Status {
	case "FAIL":
		return provider.ActionFailed, nil
	case "SUCCESS":
		if expectedSize == 0 || (response.Entities.ID == volumeID && (response.Entities.Size == nil || *response.Entities.Size == expectedSize)) {
			return provider.ActionSucceeded, nil
		}
	case "RUNNING", "INIT", "WAITING_EXECUTE", "LISTENING":
		return provider.ActionPending, nil
	}
	return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
}
