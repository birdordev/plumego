package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/kubernetes"
)

type clusterWorkloadProfile struct {
	CloudIdentity      string `json:"cloud_identity"`
	CredentialRevision int64  `json:"credential_revision"`
	kubernetes.Config
}

func runClusterWorkloadRefresh(parent context.Context, db *sql.DB, args []string, cloud string, profile publicCloudProfile) error {
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()
	task := controller.Task{
		Key:      inventory.Key{Tenant: args[1], Account: args[2], Region: args[3], Kind: provider.KubernetesCluster},
		TargetID: args[4], Provider: cloud, CredentialRef: profile.Ref,
	}
	publish, err := prepareWorkloadRefresh(ctx, db, task, profile)
	if err != nil {
		return err
	}
	err = dbutil.WithTransaction(ctx, db, nil, func(tx *sql.Tx) error { return publish(ctx, tx) })
	return inventoryCommandError(err)
}

func prepareScheduledWorkloads(ctx context.Context, db *sql.DB, task controller.Task, path string) (controller.Publish, error) {
	var profile publicCloudProfile
	if err := loadPrivateProfile(path, &profile); err != nil {
		return nil, err
	}
	return prepareWorkloadRefresh(ctx, db, task, profile)
}

func prepareWorkloadRefresh(ctx context.Context, db *sql.DB, task controller.Task, profile publicCloudProfile) (controller.Publish, error) {
	switch task.Provider {
	case provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS:
	default:
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	key := task.Key
	binding, err := identity.NewStore(db).CloudAccountBinding(ctx, key.Tenant, key.Account)
	if err != nil || (binding.Mode != "read_only" && binding.Mode != "write_enabled") ||
		binding.Provider != task.Provider || binding.RemoteIdentity == "" ||
		(task.CredentialRevision > 0 && binding.Revision != task.CredentialRevision) ||
		task.CredentialRef == "" || binding.CredentialRef != task.CredentialRef || profile.Ref != task.CredentialRef {
		return nil, errors.New("provider account or credential binding unavailable")
	}
	path := profile.KubernetesWorkloads[key.Region+"/"+task.TargetID]
	if !filepath.IsAbs(path) {
		return nil, errors.New("Kubernetes workload profile unavailable")
	}
	var workload clusterWorkloadProfile
	if err := loadPrivateProfile(path, &workload); err != nil {
		return nil, err
	}
	if workload.CloudIdentity != binding.RemoteIdentity || workload.CredentialRevision != binding.Revision {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	client, err := kubernetes.NewClient(workload.Config)
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	store := inventory.NewStore(db)
	if task.ScheduleKind == controller.WorkloadWatchScheduleKind {
		return store.PrepareClusterWorkloadWatch(ctx, key, task.TargetID, binding.Revision, client.ObservationBinding(), client.ObserveWorkloads)
	}
	return store.PrepareClusterWorkloads(ctx, key, task.TargetID, binding.Revision, client.ReadWorkloads)
}
