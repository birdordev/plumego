// Package filter provides content filtering for AI inputs and outputs.
package filter

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Filter is the interface for content filters.
type Filter interface {
	// Name returns the filter name.
	Name() string

	// Filter checks the content and returns a result.
	Filter(ctx context.Context, content string) (*Result, error)
}

// Result represents a filter result.
type Result struct {
	// Whether the content is allowed
	Allowed bool

	// Reason for blocking (if not allowed)
	Reason string

	// Confidence score (0.0 to 1.0)
	Score float64

	// Labels/categories detected
	Labels []string

	// Matches found (for debugging)
	Matches []Match

	// Filter that produced this result
	FilterName string
}

// Match represents a detected pattern.
type Match struct {
	Pattern string
	Text    string
	Start   int
	End     int
}

// Stage represents filtering stage.
type Stage string

const (
	// StageInput filters user input
	StageInput Stage = "input"

	// StageOutput filters AI output
	StageOutput Stage = "output"
)

// Chain applies multiple filters in sequence.
type Chain struct {
	filters []Filter
	policy  Policy
}

// NewChain creates a new filter chain.
func NewChain(policy Policy, filters ...Filter) *Chain {
	return &Chain{
		filters: filters,
		policy:  policy,
	}
}

// Filter applies all filters in the chain.
func (c *Chain) Filter(ctx context.Context, content string, stage Stage) (*Result, error) {
	combinedResult := &Result{
		Allowed: true,
		Score:   0.0,
	}

	for _, filter := range c.filters {
		result, err := filter.Filter(ctx, content)
		if err != nil {
			return nil, fmt.Errorf("filter %s: %w", filter.Name(), err)
		}

		// Combine results
		if !result.Allowed {
			combinedResult.Allowed = false
			combinedResult.Reason = result.Reason
			combinedResult.FilterName = result.FilterName
		}

		// Merge labels
		combinedResult.Labels = append(combinedResult.Labels, result.Labels...)

		// Merge matches
		combinedResult.Matches = append(combinedResult.Matches, result.Matches...)

		// Update score (use maximum)
		if result.Score > combinedResult.Score {
			combinedResult.Score = result.Score
		}

		// Check policy
		action := c.policy.GetAction(result, stage)
		if action == ActionBlock {
			combinedResult.Allowed = false
			return combinedResult, nil
		}
	}

	return combinedResult, nil
}

// Policy defines filtering policy.
type Policy interface {
	// GetAction returns the action to take for a filter result.
	GetAction(result *Result, stage Stage) Action

	// ShouldBlock checks if a result should block the content.
	ShouldBlock(result *Result) bool
}

// Action represents the action to take.
type Action string

const (
	// ActionAllow allows the content
	ActionAllow Action = "allow"

	// ActionBlock blocks the content
	ActionBlock Action = "block"

	// ActionWarn logs a warning but allows
	ActionWarn Action = "warn"
)

// StrictPolicy blocks any filtered content.
type StrictPolicy struct{}

// GetAction implements Policy.
func (p *StrictPolicy) GetAction(result *Result, stage Stage) Action {
	if !result.Allowed {
		return ActionBlock
	}
	return ActionAllow
}

// ShouldBlock implements Policy.
func (p *StrictPolicy) ShouldBlock(result *Result) bool {
	return !result.Allowed
}

// PermissivePolicy only blocks high-confidence matches.
type PermissivePolicy struct {
	Threshold float64 // Block if score >= threshold
}

// GetAction implements Policy.
func (p *PermissivePolicy) GetAction(result *Result, stage Stage) Action {
	if !result.Allowed && result.Score >= p.Threshold {
		return ActionBlock
	}
	if !result.Allowed {
		return ActionWarn
	}
	return ActionAllow
}

// ShouldBlock implements Policy.
func (p *PermissivePolicy) ShouldBlock(result *Result) bool {
	return !result.Allowed && result.Score >= p.Threshold
}

var piiPatterns = map[string]*regexp.Regexp{
	"email":       regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),
	"phone":       regexp.MustCompile(`\b\d{3}[-.]?\d{3}[-.]?\d{4}\b`),
	"ssn":         regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
	"credit_card": regexp.MustCompile(`\b\d{4}[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4}\b`),
	"ip_address":  regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`),
}

// PIIFilter detects personally identifiable information.
type PIIFilter struct{}

// NewPIIFilter creates a new PII filter.
func NewPIIFilter() *PIIFilter {
	return &PIIFilter{}
}

// Name implements Filter.
func (f *PIIFilter) Name() string {
	return "pii"
}

// Filter implements Filter.
func (f *PIIFilter) Filter(ctx context.Context, content string) (*Result, error) {
	result := &Result{
		Allowed:    true,
		FilterName: f.Name(),
	}

	for label, pattern := range piiPatterns {
		matches := pattern.FindAllStringIndex(content, -1)
		if len(matches) > 0 {
			result.Allowed = false
			result.Reason = fmt.Sprintf("detected %s", label)
			result.Labels = append(result.Labels, label)
			result.Score = 0.9 // High confidence

			for _, match := range matches {
				result.Matches = append(result.Matches, Match{
					Pattern: label,
					Text:    content[match[0]:match[1]],
					Start:   match[0],
					End:     match[1],
				})
			}
		}
	}

	return result, nil
}

var secretPatterns = map[string]*regexp.Regexp{
	"api_key":      regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*['"]?([a-zA-Z0-9_-]{20,})['"]?`),
	"aws_key":      regexp.MustCompile(`(?i)(AKIA[0-9A-Z]{16})`),
	"github_token": regexp.MustCompile(`(?i)(ghp|gho|ghu|ghs|ghr)_[a-zA-Z0-9]{36,}`),
	"slack_token":  regexp.MustCompile(`xox[baprs]-[0-9]{10,13}-[0-9]{10,13}-[a-zA-Z0-9]{24,}`),
	"jwt":          regexp.MustCompile(`eyJ[a-zA-Z0-9_-]+\.eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+`),
	"private_key":  regexp.MustCompile(`-----BEGIN (RSA |EC |DSA )?PRIVATE KEY-----`),
	"password":     regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*['"]?([^'"\s]{8,})['"]?`),
}

// SecretFilter detects secrets and credentials.
type SecretFilter struct{}

// NewSecretFilter creates a new secret filter.
func NewSecretFilter() *SecretFilter {
	return &SecretFilter{}
}

// Name implements Filter.
func (f *SecretFilter) Name() string {
	return "secret"
}

// Filter implements Filter.
func (f *SecretFilter) Filter(ctx context.Context, content string) (*Result, error) {
	result := &Result{
		Allowed:    true,
		FilterName: f.Name(),
	}

	for label, pattern := range secretPatterns {
		matches := pattern.FindAllStringIndex(content, -1)
		if len(matches) > 0 {
			result.Allowed = false
			result.Reason = fmt.Sprintf("detected %s", label)
			result.Labels = append(result.Labels, label)
			result.Score = 1.0 // Very high confidence

			for _, match := range matches {
				// Never copy the matched secret into the result: Matches
				// report position only, with the text redacted.
				result.Matches = append(result.Matches, Match{
					Pattern: label,
					Text:    "[REDACTED]",
					Start:   match[0],
					End:     match[1],
				})
			}
		}
	}

	return result, nil
}

// PromptInjectionFilter detects prompt injection attempts.
type PromptInjectionFilter struct {
	patterns []string
}

// NewPromptInjectionFilter creates a new prompt injection filter.
func NewPromptInjectionFilter() *PromptInjectionFilter {
	return &PromptInjectionFilter{
		patterns: []string{
			"ignore previous instructions",
			"ignore all previous",
			"disregard all previous",
			"forget everything",
			"new instructions",
			"system:",
			"<|im_start|>",
			"<|im_end|>",
			"[INST]",
			"[/INST]",
			"\\n\\nSystem:",
		},
	}
}

// Name implements Filter.
func (f *PromptInjectionFilter) Name() string {
	return "prompt_injection"
}

// Filter implements Filter.
func (f *PromptInjectionFilter) Filter(ctx context.Context, content string) (*Result, error) {
	result := &Result{
		Allowed:    true,
		FilterName: f.Name(),
	}

	lowerContent := strings.ToLower(content)

	for _, pattern := range f.patterns {
		if strings.Contains(lowerContent, pattern) {
			result.Allowed = false
			result.Reason = "detected prompt injection attempt"
			result.Labels = append(result.Labels, "prompt_injection")
			result.Score = 0.8

			// Find position
			idx := strings.Index(lowerContent, pattern)
			if idx >= 0 {
				result.Matches = append(result.Matches, Match{
					Pattern: pattern,
					Text:    content[idx : idx+len(pattern)],
					Start:   idx,
					End:     idx + len(pattern),
				})
			}
		}
	}

	return result, nil
}

// ProfanityFilter detects profanity (basic implementation).
type ProfanityFilter struct {
	words []string
}

// NewProfanityFilter creates a new profanity filter.
func NewProfanityFilter(words []string) *ProfanityFilter {
	if words == nil {
		words = defaultProfanityList()
	}
	return &ProfanityFilter{
		words: words,
	}
}

// Name implements Filter.
func (f *ProfanityFilter) Name() string {
	return "profanity"
}

// Filter implements Filter.
func (f *ProfanityFilter) Filter(ctx context.Context, content string) (*Result, error) {
	result := &Result{
		Allowed:    true,
		FilterName: f.Name(),
	}

	lowerContent := strings.ToLower(content)

	for _, word := range f.words {
		if strings.Contains(lowerContent, word) {
			result.Allowed = false
			result.Reason = "detected profanity"
			result.Labels = append(result.Labels, "profanity")
			result.Score = 0.7

			idx := strings.Index(lowerContent, word)
			if idx >= 0 {
				result.Matches = append(result.Matches, Match{
					Pattern: "profanity",
					Text:    "[PROFANITY]",
					Start:   idx,
					End:     idx + len(word),
				})
			}
		}
	}

	return result, nil
}

// defaultProfanityList returns a basic profanity list.
func defaultProfanityList() []string {
	// This is a minimal list for demonstration
	// In production, use a comprehensive profanity database
	return []string{
		// Add actual profanity words as needed
	}
}

// RedactContent redacts sensitive content based on filter results.
func RedactContent(content string, result *Result) string {
	if result == nil || result.Allowed || len(result.Matches) == 0 {
		return content
	}

	// Sort matches by position (descending) to avoid offset issues.
	matches := make([]Match, len(result.Matches))
	copy(matches, result.Matches)
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Start == matches[j].Start {
			return matches[i].End > matches[j].End
		}
		return matches[i].Start > matches[j].Start
	})

	// Simple redaction: replace matches with [REDACTED]
	redacted := content
	for _, match := range matches {
		if match.Start < 0 || match.End < match.Start || match.End > len(redacted) {
			// Ignore malformed/outdated ranges instead of panicking.
			continue
		}
		redacted = redacted[:match.Start] + "[REDACTED]" + redacted[match.End:]
	}

	return redacted
}
