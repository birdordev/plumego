package main

import (
	"context"
	"crypto/subtle"
	"path/filepath"
	"slices"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/kubernetes"
)

type workloadScaleProfile struct {
	clusterWorkloadProfile
	MaxReplicas int `json:"max_replicas"`
}

func connectWorkloadScaler(ctx context.Context, binding identity.CloudAccountBinding, path, region, cluster string) (provider.WorkloadScaler, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if binding.Provider != provider.TencentCVM && binding.Provider != provider.AlibabaECS && binding.Provider != provider.HuaweiECS {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	// 只读续查可以在账号冻结写入后继续执行，发送前的事务另行核对账号模式。
	if binding.RemoteIdentity == "" || binding.CredentialRef == "" {
		return nil, identity.ErrForbidden
	}
	var profile publicCloudProfile
	if err := loadPrivateProfile(path, &profile); err != nil {
		return nil, err
	}
	scope := region + "/" + cluster
	readerPath, writerPath := profile.KubernetesWorkloads[scope], profile.KubernetesWorkloadWriters[scope]
	if profile.Ref != binding.CredentialRef || !filepath.IsAbs(readerPath) || !filepath.IsAbs(writerPath) || readerPath == writerPath {
		return nil, identity.ErrForbidden
	}
	var reader clusterWorkloadProfile
	if err := loadPrivateProfile(readerPath, &reader); err != nil {
		return nil, err
	}
	var writer workloadScaleProfile
	if err := loadPrivateProfile(writerPath, &writer); err != nil {
		return nil, err
	}
	if reader.CloudIdentity != binding.RemoteIdentity || writer.CloudIdentity != binding.RemoteIdentity ||
		reader.CredentialRevision != binding.Revision || writer.CredentialRevision != binding.Revision ||
		!workloadScaleBindingMatches(reader.Config, writer.Config) {
		return nil, identity.ErrForbidden
	}
	return kubernetes.NewScaleClient(writer.Config, writer.MaxReplicas)
}

func workloadScaleBindingMatches(reader, writer kubernetes.Config) bool {
	if reader.Endpoint != writer.Endpoint || reader.CA != writer.CA ||
		reader.IdentityNamespace != writer.IdentityNamespace || reader.IdentityUID != writer.IdentityUID ||
		subtle.ConstantTimeCompare([]byte(reader.Token), []byte(writer.Token)) == 1 {
		return false
	}
	for _, namespace := range writer.Namespaces {
		if !slices.Contains(reader.Namespaces, namespace) {
			return false
		}
	}
	return true
}
