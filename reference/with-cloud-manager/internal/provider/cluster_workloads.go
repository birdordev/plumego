package provider

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

const MaxClusterWorkloads = 5000

type ClusterWorkload struct {
	UID       string `json:"uid"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Desired   *int   `json:"desired,omitempty"`
	Ready     *int   `json:"ready,omitempty"`
	Active    *int   `json:"active,omitempty"`
	Succeeded *int   `json:"succeeded,omitempty"`
	Failed    *int   `json:"failed,omitempty"`
	Suspended *bool  `json:"suspended,omitempty"`
}

var workloadLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var workloadNamePart = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
var workloadUID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func ValidWorkloadNamespace(name string) bool { return workloadLabel.MatchString(name) }
func ValidWorkloadUID(uid string) bool        { return workloadUID.MatchString(uid) }

func NormalizeClusterWorkloads(items []ClusterWorkload) error {
	invalid := &Failure{Kind: InvalidResponse}
	if items == nil || len(items) > MaxClusterWorkloads {
		return invalid
	}
	uids := make(map[string]struct{}, len(items))
	for _, item := range items {
		if !item.Valid() {
			return invalid
		}
		if _, found := uids[item.UID]; found {
			return invalid
		}
		uids[item.UID] = struct{}{}
	}
	slices.SortFunc(items, compareWorkloads)
	for i := 1; i < len(items); i++ {
		if compareWorkloads(items[i-1], items[i]) == 0 {
			return invalid
		}
	}
	return nil
}

func compareWorkloads(a, b ClusterWorkload) int {
	return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name))
}

func (w ClusterWorkload) Valid() bool {
	if !ValidWorkloadNamespace(w.Namespace) || !ValidWorkloadUID(w.UID) || w.Name == "" || len(w.Name) > 253 {
		return false
	}
	for label := range strings.SplitSeq(w.Name, ".") {
		if !workloadNamePart.MatchString(label) {
			return false
		}
	}
	switch w.Kind {
	case "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob":
	default:
		return false
	}
	for _, count := range [...]*int{w.Desired, w.Ready, w.Active, w.Succeeded, w.Failed} {
		if count != nil && (*count < 0 || *count > 1_000_000) {
			return false
		}
	}
	return true
}

// WorkloadVersion 是单个命名空间和资源类型的订阅检查点。
type WorkloadVersion struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Version   string `json:"version"`
}

type WorkloadObservation struct {
	Items    []ClusterWorkload
	Versions []WorkloadVersion
}
