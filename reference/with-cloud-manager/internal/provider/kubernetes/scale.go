package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"

	"with-cloud-manager/internal/provider"
)

type ScaleClient struct {
	client      *Client
	maxReplicas int
}

func NewScaleClient(cfg Config, maxReplicas int) (*ScaleClient, error) {
	if maxReplicas < 1 || maxReplicas > provider.MaxWorkloadScaleReplicas {
		return nil, errors.New("invalid Kubernetes workload scale limit")
	}
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &ScaleClient{client: client, maxReplicas: maxReplicas}, nil
}

func (c *ScaleClient) CloseIdleConnections() { c.client.CloseIdleConnections() }

type scaleObject struct {
	typeMeta
	Metadata objectMeta `json:"metadata"`
	Spec     scaleSpec  `json:"spec"`
}

type scaleSpec struct {
	Replicas *int `json:"replicas"`
}

func (c *ScaleClient) scalePath(change provider.WorkloadScaleChange) (string, error) {
	if !change.Valid() || !slices.Contains(c.client.config.Namespaces, change.Namespace) {
		return "", &provider.Failure{Kind: provider.Unauthorized}
	}
	for _, source := range workloadCollections {
		if source.kind == change.Kind {
			return (workloadSource{namespace: change.Namespace, collection: source}).path() + "/" + change.Name + "/scale", nil
		}
	}
	return "", &provider.Failure{Kind: provider.Unsupported}
}

func (c *ScaleClient) readScale(ctx context.Context, change provider.WorkloadScaleChange) (string, scaleObject, error) {
	path, err := c.scalePath(change)
	if err != nil {
		return "", scaleObject{}, err
	}
	if err := c.client.checkIdentity(ctx); err != nil {
		return "", scaleObject{}, err
	}
	var scale scaleObject
	if err := c.client.get(ctx, path, nil, 64<<10, nil, &scale); err != nil {
		return "", scaleObject{}, err
	}
	if err := validateScale(scale, change); err != nil {
		return "", scaleObject{}, err
	}
	return path, scale, nil
}

func validateScale(scale scaleObject, change provider.WorkloadScaleChange) error {
	meta := scale.Metadata
	if scale.Kind != "Scale" || scale.Version != "autoscaling/v1" || meta.Namespace != change.Namespace || meta.Name != change.Name ||
		!validResourceVersion(meta.ResourceVersion) || scale.Spec.Replicas == nil || *scale.Spec.Replicas < 0 || *scale.Spec.Replicas > 1_000_000 {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if meta.UID != change.UID {
		return &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return nil
}

func (c *ScaleClient) PrepareWorkloadScale(ctx context.Context, change provider.WorkloadScaleChange) (func(context.Context) (string, error), error) {
	if change.Desired > c.maxReplicas {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	path, scale, err := c.readScale(ctx, change)
	if err != nil {
		return nil, err
	}
	if *scale.Spec.Replicas != change.Current {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	scale.Spec.Replicas = &change.Desired
	body, err := json.Marshal(scale)
	if err != nil {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return func(ctx context.Context) (string, error) {
		resp, err := c.client.sendJSON(ctx, http.MethodPut, path, nil, body)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		var saved scaleObject
		bounded := &io.LimitedReader{R: resp.Body, N: (64 << 10) + 1}
		decoder := json.NewDecoder(bounded)
		if decoder.Decode(&saved) != nil || decoder.Decode(new(json.RawMessage)) != io.EOF || bounded.N == 0 ||
			validateScale(saved, change) != nil || *saved.Spec.Replicas != change.Desired {
			return "", &provider.Failure{Kind: provider.OutcomeUnknown}
		}
		return "", nil
	}, nil
}

func (c *ScaleClient) ObserveWorkloadScale(ctx context.Context, change provider.WorkloadScaleChange) (provider.ActionResult, error) {
	_, scale, err := c.readScale(ctx, change)
	if err != nil {
		return "", err
	}
	switch *scale.Spec.Replicas {
	case change.Desired:
		return provider.ActionSucceeded, nil
	case change.Current:
		return provider.ActionPending, nil
	default:
		return "", &provider.Failure{Kind: provider.PreconditionFailed}
	}
}
