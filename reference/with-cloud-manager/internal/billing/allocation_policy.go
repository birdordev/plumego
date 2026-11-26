package billing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxAllocationRules   = 64
	maxAllocationCenters = 32
	maxAllocationShares  = 8
	allocationWeight     = 10000
	unallocatedCenter    = "unallocated"
	allocationAlgorithm  = "weighted-decimal-v1"
)

var centerID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type AllocationPolicy struct {
	Rules   []AllocationRule  `json:"rules,omitempty"`
	Default []AllocationShare `json:"default,omitempty"`
}

type AllocationRule struct {
	Resource string                     `json:"resource,omitempty"`
	Service  string                     `json:"service,omitempty"`
	Region   string                     `json:"region,omitempty"`
	Category string                     `json:"category,omitempty"`
	Tags     map[string]json.RawMessage `json:"tags,omitempty"`
	Shares   []AllocationShare          `json:"shares"`
}

type AllocationShare struct {
	Center string `json:"center"`
	Weight int    `json:"weight"`
}

func canonicalPolicy(policy AllocationPolicy) (AllocationPolicy, []byte, error) {
	if len(policy.Rules) > maxAllocationRules {
		return AllocationPolicy{}, nil, fmt.Errorf("%w: too many allocation rules", ErrInvalid)
	}
	out := AllocationPolicy{Rules: slices.Clone(policy.Rules)}
	centers := make(map[string]bool)
	for i, rule := range out.Rules {
		if !validAllocationRule(rule) {
			return AllocationPolicy{}, nil, fmt.Errorf("%w: invalid selector in rule %d", ErrInvalid, i+1)
		}
		tags := make(map[string]json.RawMessage, len(rule.Tags))
		for key, value := range rule.Tags {
			scalar, ok := tagScalar(value)
			if !validSelector(key, 256) || key == "" || !ok {
				return AllocationPolicy{}, nil, fmt.Errorf("%w: invalid tag in rule %d", ErrInvalid, i+1)
			}
			tags[key] = json.RawMessage(scalar)
		}
		rule.Tags = tags
		shares, err := canonicalShares(rule.Shares, centers)
		if err != nil {
			return AllocationPolicy{}, nil, fmt.Errorf("rule %d: %w", i+1, err)
		}
		rule.Shares = shares
		out.Rules[i] = rule
	}
	if len(policy.Default) != 0 {
		shares, err := canonicalShares(policy.Default, centers)
		if err != nil {
			return AllocationPolicy{}, nil, fmt.Errorf("default: %w", err)
		}
		out.Default = shares
	}
	if len(centers) > maxAllocationCenters {
		return AllocationPolicy{}, nil, fmt.Errorf("%w: too many cost centers", ErrInvalid)
	}
	raw, err := json.Marshal(out)
	if err == nil && len(raw) > MaxAllocationPolicyBytes {
		err = fmt.Errorf("%w: allocation policy exceeds size limit", ErrInvalid)
	}
	return out, raw, err
}

func validAllocationRule(rule AllocationRule) bool {
	return (rule.Resource != "" || rule.Service != "" || rule.Region != "" || rule.Category != "" || len(rule.Tags) != 0) &&
		validSelector(rule.Resource, 1024) && validSelector(rule.Service, 256) &&
		validSelector(rule.Region, 128) && validSelector(rule.Category, 128) && len(rule.Tags) <= 8
}

func validSelector(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && strings.TrimSpace(value) == value
}

func canonicalShares(shares []AllocationShare, centers map[string]bool) ([]AllocationShare, error) {
	seen := make(map[string]bool)
	sum := 0
	if len(shares) == 0 || len(shares) > maxAllocationShares {
		return nil, fmt.Errorf("%w: invalid share count", ErrInvalid)
	}
	for _, share := range shares {
		if !centerID.MatchString(share.Center) || share.Center == unallocatedCenter || seen[share.Center] ||
			share.Weight < 1 || share.Weight > allocationWeight {
			return nil, fmt.Errorf("%w: invalid cost center or weight", ErrInvalid)
		}
		sum += share.Weight
		seen[share.Center], centers[share.Center] = true, true
	}
	if sum != allocationWeight {
		return nil, fmt.Errorf("%w: weights must total 10000 basis points", ErrInvalid)
	}
	result := slices.Clone(shares)
	slices.SortFunc(result, func(a, b AllocationShare) int { return strings.Compare(a.Center, b.Center) })
	return result, nil
}

func tagScalar(raw json.RawMessage) (string, bool) {
	var value any
	if !utf8.Valid(raw) || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	switch value := value.(type) {
	case nil, bool:
		return string(bytes.TrimSpace(raw)), true
	case string:
		if len(value) > 4096 || strings.ContainsRune(value, 0) {
			return "", false
		}
		encoded, err := json.Marshal(value)
		return string(encoded), err == nil
	default:
		return "", false
	}
}

func readAllocationTags(raw string) (map[string]string, bool) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	open, err := decoder.Token()
	if err != nil || open != json.Delim('{') {
		return nil, false
	}
	result := make(map[string]string)
	for decoder.More() {
		token, err := decoder.Token()
		key, valid := token.(string)
		if err != nil || !valid {
			return nil, false
		}
		if _, seen := result[key]; seen {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		scalar, ok := tagScalar(value)
		if !ok {
			return nil, false
		}
		result[key] = scalar
	}
	if closeToken, err := decoder.Token(); err != nil || closeToken != json.Delim('}') {
		return nil, false
	}
	_, err = decoder.Token()
	return result, err == io.EOF
}
