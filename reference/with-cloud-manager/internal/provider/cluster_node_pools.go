package provider

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const MaxClusterNodePools = 1000

// ClusterNodePool 是父集群下的只读投影。可选数值保留已知零值和未知值的区别。
type ClusterNodePool struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"`
	Status       string `json:"status,omitempty"`
	CurrentNodes *int   `json:"current_nodes,omitempty"`
	DesiredNodes *int   `json:"desired_nodes,omitempty"`
	MinNodes     *int   `json:"min_nodes,omitempty"`
	MaxNodes     *int   `json:"max_nodes,omitempty"`
	Autoscaling  *bool  `json:"autoscaling,omitempty"`
}

type ClusterNodePoolReader interface {
	ReadClusterNodePools(ctx context.Context, scope Scope, clusterID string) ([]ClusterNodePool, error)
}

var clusterNodePoolID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func NormalizeClusterNodePools(pools []ClusterNodePool) error {
	if pools == nil || len(pools) > MaxClusterNodePools {
		return &Failure{Kind: InvalidResponse}
	}
	for _, pool := range pools {
		if !pool.valid() {
			return &Failure{Kind: InvalidResponse}
		}
	}
	slices.SortFunc(pools, func(a, b ClusterNodePool) int { return strings.Compare(a.ID, b.ID) })
	for i := 1; i < len(pools); i++ {
		if pools[i-1].ID == pools[i].ID {
			return &Failure{Kind: InvalidResponse}
		}
	}
	return nil
}

func (pool ClusterNodePool) valid() bool {
	if !clusterNodePoolID.MatchString(pool.ID) || strings.TrimSpace(pool.Name) == "" || len(pool.Name) > 512 ||
		strings.ContainsFunc(pool.Name, unicode.IsControl) ||
		(pool.Type != "" && !clusterType.MatchString(pool.Type)) ||
		(pool.Status != "" && !clusterType.MatchString(pool.Status)) {
		return false
	}
	for _, count := range [...]*int{pool.CurrentNodes, pool.DesiredNodes, pool.MinNodes, pool.MaxNodes} {
		if count != nil && (*count < 0 || *count > 1_000_000) {
			return false
		}
	}
	return pool.MinNodes == nil || pool.MaxNodes == nil || *pool.MinNodes <= *pool.MaxNodes
}
