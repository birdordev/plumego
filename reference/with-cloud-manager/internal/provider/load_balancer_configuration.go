package provider

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxLoadBalancerComponents = 1000

const (
	LoadBalancerDefaultGroup          = "default"
	LoadBalancerPrimarySecondaryGroup = "primary-secondary"
	LoadBalancerVirtualGroup          = "virtual"
	LoadBalancerDirectGroup           = "direct"
)

// LoadBalancerConfiguration contains only components associated with the requested instance.
type LoadBalancerConfiguration struct {
	Listeners    []LoadBalancerListener       `json:"listeners"`
	TargetGroups []LoadBalancerTargetGroup    `json:"target_groups"`
	Backends     *LoadBalancerBackendSnapshot `json:"backends,omitempty"`
}

type LoadBalancerListener struct {
	ID             string   `json:"id"`
	Name           string   `json:"name,omitempty"`
	Protocol       string   `json:"protocol"`
	Port           int      `json:"port"`
	EndPort        int      `json:"end_port,omitempty"`
	TargetGroupIDs []string `json:"target_group_ids,omitempty"`
}

type LoadBalancerTargetGroup struct {
	AllPorts  bool   `json:"-"`
	Exclusive bool   `json:"exclusive,omitempty"`
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Algorithm string `json:"algorithm,omitempty"`
}

type LoadBalancerConfigurationReader interface {
	ReadLoadBalancerConfiguration(ctx context.Context, scope Scope, request LoadBalancerConfigurationRequest) (LoadBalancerConfiguration, error)
}

type LoadBalancerConfigurationRequest struct {
	ID              string
	IncludeBackends bool
	BackendSecret   []byte `json:"-"`
}

func (cfg *LoadBalancerConfiguration) Normalize() error {
	if cfg.Listeners == nil {
		cfg.Listeners = []LoadBalancerListener{}
	}
	if cfg.TargetGroups == nil {
		cfg.TargetGroups = []LoadBalancerTargetGroup{}
	}
	if len(cfg.Listeners)+len(cfg.TargetGroups) > MaxLoadBalancerComponents {
		return &Failure{Kind: InvalidResponse}
	}
	groups := make(map[string]string, len(cfg.TargetGroups))
	for i := range cfg.TargetGroups {
		group := &cfg.TargetGroups[i]
		group.Protocol = strings.ToUpper(group.Protocol)
		group.Algorithm = strings.ToUpper(group.Algorithm)
		if _, duplicate := groups[group.ID]; duplicate || !validLoadBalancerComponentID(group.ID) ||
			!validLoadBalancerComponentText(group.Name, 1024) ||
			!validLoadBalancerComponentToken(group.Protocol) || !validLoadBalancerComponentToken(group.Algorithm) || (group.Kind != "" && group.Exclusive) {
			return &Failure{Kind: InvalidResponse}
		}
		switch group.Kind {
		case "", LoadBalancerDefaultGroup, LoadBalancerPrimarySecondaryGroup, LoadBalancerVirtualGroup, LoadBalancerDirectGroup:
		default:
			return &Failure{Kind: InvalidResponse}
		}
		groups[group.ID] = group.Kind
	}
	listeners := make(map[string]struct{}, len(cfg.Listeners))
	linkCount := 0
	for i := range cfg.Listeners {
		listener := &cfg.Listeners[i]
		listener.Protocol = strings.ToUpper(listener.Protocol)
		linkCount += len(listener.TargetGroupIDs)
		if _, duplicate := listeners[listener.ID]; duplicate || !ValidLoadBalancerListenerID(listener.ID) ||
			!validLoadBalancerComponentText(listener.Name, 1024) ||
			listener.Protocol == "" || !validLoadBalancerComponentToken(listener.Protocol) ||
			listener.Port < 1 || listener.Port > 65535 ||
			(listener.EndPort != 0 && (listener.EndPort < listener.Port || listener.EndPort > 65535)) ||
			linkCount > 8*MaxLoadBalancerComponents {
			return &Failure{Kind: InvalidResponse}
		}
		listeners[listener.ID] = struct{}{}
		slices.Sort(listener.TargetGroupIDs)
		listener.TargetGroupIDs = slices.Compact(listener.TargetGroupIDs)
		for _, id := range listener.TargetGroupIDs {
			if _, exists := groups[id]; !exists {
				return &Failure{Kind: InvalidResponse}
			}
		}
	}
	slices.SortFunc(cfg.Listeners, func(a, b LoadBalancerListener) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(cfg.TargetGroups, func(a, b LoadBalancerTargetGroup) int { return strings.Compare(a.ID, b.ID) })
	if cfg.Backends != nil {
		return cfg.Backends.normalize(cfg.Listeners, groups)
	}
	return nil
}

func ValidLoadBalancerListenerID(id string) bool {
	base, port, hasPort := strings.Cut(id, "@")
	if !hasPort {
		return validLoadBalancerComponentID(base)
	}
	number, err := strconv.ParseUint(port, 10, 16)
	return len(id) <= MaxObjectIDSize && strings.HasPrefix(base, "lsn-") && len(base) > 4 &&
		validLoadBalancerComponentID(base) && err == nil && strconv.FormatUint(number, 10) == port
}

func ValidLoadBalancerTargetGroupID(id string) bool {
	return validLoadBalancerComponentID(id)
}

func validLoadBalancerComponentID(value string) bool {
	return value != "" && len(value) <= MaxObjectIDSize && loadBalancerCharacters(value, "-_.")
}

func validLoadBalancerComponentToken(value string) bool {
	return len(value) <= 48 && loadBalancerCharacters(value, "-_/")
}

func validLoadBalancerComponentText(value string, length int) bool {
	return len(value) <= length && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func loadBalancerCharacters(value, punctuation string) bool {
	return !strings.ContainsFunc(value, func(r rune) bool {
		return (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune(punctuation, r)
	})
}
