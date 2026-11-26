package provider

import (
	"regexp"
	"strings"
)

const (
	TencentTKE = "tencent-tke"
	AlibabaACK = "alibaba-ack"
	HuaweiCCE  = "huawei-cce"
)

// ClusterDetails 保存集群管理面元数据。节点数沿用厂商的计数定义。
type ClusterDetails struct {
	Product   string `json:"product"`
	Type      string `json:"cluster_type"`
	Version   string `json:"version,omitempty"`
	NodeCount *int   `json:"node_count,omitempty"`
}

var clusterType = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,79}$`)

func (o Object) ValidCluster() bool {
	cluster := o.Cluster
	if cluster == nil || !o.validRegionalTopology() || o.Tags != nil || o.Snapshot != nil ||
		o.Bucket != nil || o.Database != nil || o.Cache != nil || o.LoadBalancer != nil {
		return false
	}
	switch cluster.Product {
	case TencentTKE, AlibabaACK, HuaweiCCE:
	default:
		return false
	}
	return clusterType.MatchString(cluster.Type) &&
		(cluster.Version == "" || engineVersion.MatchString(cluster.Version)) &&
		(cluster.NodeCount == nil || (*cluster.NodeCount >= 0 && *cluster.NodeCount <= 1_000_000))
}

func (o Object) validRegionalTopology() bool {
	if o.Status == "" || strings.ContainsRune(o.Status, 0) || strings.ContainsRune(o.Name, 0) ||
		len(o.PublicAddresses) != 0 || len(o.Links) > 1 {
		return false
	}
	if len(o.Links) == 0 {
		return true
	}
	link := o.Links[0]
	return link.Kind == Network && link.Relation == "in_network" && link.ID != "" && !strings.ContainsRune(link.ID, 0)
}
