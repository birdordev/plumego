package provider

import "context"

const MaxWorkloadScaleReplicas = 100

type WorkloadScaleTarget struct {
	UID       string `json:"uid"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
}

func (t WorkloadScaleTarget) Valid() bool {
	item := ClusterWorkload{UID: t.UID, Namespace: t.Namespace, Kind: t.Kind, Name: t.Name}
	return (t.Kind == "Deployment" || t.Kind == "StatefulSet") && item.Valid()
}

type WorkloadScaleChange struct {
	WorkloadScaleTarget
	Current int `json:"current_replicas"`
	Desired int `json:"replicas"`
}

func (c WorkloadScaleChange) Valid() bool {
	return c.WorkloadScaleTarget.Valid() && c.Current >= 0 && c.Current <= 1_000_000 &&
		c.Desired > 0 && c.Desired <= MaxWorkloadScaleReplicas && c.Current != c.Desired
}

type WorkloadScaler interface {
	PrepareWorkloadScale(context.Context, WorkloadScaleChange) (func(context.Context) (string, error), error)
	ObserveWorkloadScale(context.Context, WorkloadScaleChange) (ActionResult, error)
	CloseIdleConnections()
}
