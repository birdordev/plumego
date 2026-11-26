package provider

import (
	"cmp"
	"encoding/hex"
	"slices"
)

const MaxLoadBalancerBackendRecords = 4000

type LoadBalancerBackendSnapshot struct {
	Members []LoadBalancerBackendMember `json:"members"`
	Checks  []LoadBalancerBackendCheck  `json:"checks"`
}

type LoadBalancerBackendMember struct {
	Key            string `json:"key"`
	GroupID        string `json:"group_id"`
	Port           int    `json:"port"`
	Weight         *int   `json:"weight,omitempty"`
	Role           string `json:"role,omitempty"`
	WeightEditable bool   `json:"weight_editable,omitempty"`
}

type LoadBalancerBackendCheck struct {
	Key          string `json:"key"`
	ListenerID   string `json:"listener_id"`
	GroupID      string `json:"group_id,omitempty"`
	RuleID       string `json:"rule_id,omitempty"`
	Port         int    `json:"port"`
	ListenerPort int    `json:"listener_port,omitempty"`
	Status       string `json:"status"`
}

func (snapshot *LoadBalancerBackendSnapshot) normalize(listeners []LoadBalancerListener, groups map[string]string) error {
	if len(snapshot.Members)+len(snapshot.Checks) > MaxLoadBalancerBackendRecords {
		return &Failure{Kind: InvalidResponse}
	}
	if snapshot.Members == nil {
		snapshot.Members = []LoadBalancerBackendMember{}
	}
	if snapshot.Checks == nil {
		snapshot.Checks = []LoadBalancerBackendCheck{}
	}
	byListener := make(map[string]LoadBalancerListener, len(listeners))
	for _, listener := range listeners {
		byListener[listener.ID] = listener
	}
	seenRoles := make(map[string]uint8)
	for _, member := range snapshot.Members {
		kind, exists := groups[member.GroupID]
		if !exists || !ValidLoadBalancerBackendKey(member.Key) ||
			member.Port < 0 || member.Port > 65535 || (member.Weight != nil && (*member.Weight < 0 || *member.Weight > 100)) {
			return &Failure{Kind: InvalidResponse}
		}
		if member.WeightEditable && (kind != "" || member.Weight == nil || member.Port == 0) {
			return &Failure{Kind: InvalidResponse}
		}
		if kind == LoadBalancerPrimarySecondaryGroup {
			role := uint8(0)
			switch member.Role {
			case "primary":
				role = 1
			case "secondary":
				role = 2
			}
			if member.Port == 0 || role == 0 || seenRoles[member.GroupID]&role != 0 {
				return &Failure{Kind: InvalidResponse}
			}
			seenRoles[member.GroupID] |= role
		} else if member.Role != "" || (kind == LoadBalancerDefaultGroup && member.Port != 0) {
			return &Failure{Kind: InvalidResponse}
		}
	}
	for id, kind := range groups {
		if kind == LoadBalancerPrimarySecondaryGroup && seenRoles[id] != 3 {
			return &Failure{Kind: InvalidResponse}
		}
	}
	for _, check := range snapshot.Checks {
		listener, present := byListener[check.ListenerID]
		if !present || !ValidLoadBalancerBackendKey(check.Key) || check.Port < 0 || check.Port > 65535 ||
			(check.GroupID != "" && !slices.Contains(listener.TargetGroupIDs, check.GroupID)) ||
			(check.RuleID != "" && !validLoadBalancerComponentID(check.RuleID)) {
			return &Failure{Kind: InvalidResponse}
		}
		end := listener.Port
		if listener.EndPort != 0 {
			end = listener.EndPort
		}
		if check.ListenerPort != 0 && (check.ListenerPort < listener.Port || check.ListenerPort > end) {
			return &Failure{Kind: InvalidResponse}
		}
		switch check.Status {
		case "healthy", "unhealthy", "initial", "disabled", "unused", "unknown":
		default:
			return &Failure{Kind: InvalidResponse}
		}
	}
	slices.SortFunc(snapshot.Members, compareBackendMembers)
	for i := 1; i < len(snapshot.Members); i++ {
		if compareBackendMembers(snapshot.Members[i-1], snapshot.Members[i]) == 0 {
			return &Failure{Kind: InvalidResponse}
		}
	}
	slices.SortFunc(snapshot.Checks, compareBackendChecks)
	// 云端查询范围重叠时，同一项检查可能重复返回。
	for i := 1; i < len(snapshot.Checks); i++ {
		a, b := snapshot.Checks[i-1], snapshot.Checks[i]
		if compareBackendChecks(a, b) == 0 && a.Status != b.Status {
			return &Failure{Kind: InvalidResponse}
		}
	}
	snapshot.Checks = slices.Compact(snapshot.Checks)
	return nil
}

func ValidLoadBalancerBackendKey(value string) bool {
	if len(value) != 32 {
		return false
	}
	var decoded [16]byte
	_, err := hex.Decode(decoded[:], []byte(value))
	return err == nil
}

func compareBackendMembers(a, b LoadBalancerBackendMember) int {
	return cmp.Or(cmp.Compare(a.GroupID, b.GroupID), cmp.Compare(a.Key, b.Key), cmp.Compare(a.Port, b.Port))
}

func compareBackendChecks(a, b LoadBalancerBackendCheck) int {
	return cmp.Or(cmp.Compare(a.ListenerID, b.ListenerID), cmp.Compare(a.ListenerPort, b.ListenerPort),
		cmp.Compare(a.GroupID, b.GroupID), cmp.Compare(a.RuleID, b.RuleID), cmp.Compare(a.Key, b.Key), cmp.Compare(a.Port, b.Port))
}
