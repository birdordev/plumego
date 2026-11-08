package healthhttp

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/birdordev/plumego/health"
)

// HealthHistoryEntry represents a single aggregate health check entry.
type HealthHistoryEntry struct {
	Timestamp  time.Time          `json:"timestamp"`
	State      health.HealthState `json:"state"`
	Message    string             `json:"message"`
	Components []string           `json:"components,omitempty"`
	Duration   time.Duration      `json:"duration,omitempty"`
}

// HealthHistoryQuery represents a query for filtering health history.
type HealthHistoryQuery struct {
	StartTime *time.Time          `json:"start_time,omitempty"`
	EndTime   *time.Time          `json:"end_time,omitempty"`
	State     *health.HealthState `json:"state,omitempty"`
	Component string              `json:"component,omitempty"`
	Limit     int                 `json:"limit,omitempty"`
	Offset    int                 `json:"offset,omitempty"`
}

// HealthHistoryQueryResult represents the result of a health history query.
type HealthHistoryQueryResult struct {
	Entries []HealthHistoryEntry `json:"entries"`
	Total   int                  `json:"total"`
	Limit   int                  `json:"limit"`
	Offset  int                  `json:"offset"`
	HasMore bool                 `json:"has_more"`
}

// HistoryStats summarises collected health history.
type HistoryStats struct {
	TotalEntries   int                 `json:"total_entries"`
	OldestEntry    *HealthHistoryEntry `json:"oldest_entry,omitempty"`
	NewestEntry    *HealthHistoryEntry `json:"newest_entry,omitempty"`
	EntriesByState map[string]int      `json:"entries_by_state"`
	TimeSpan       time.Duration       `json:"time_span,omitempty"`
}

// HealthMetrics contains health-related metrics derived from tracker observations.
type HealthMetrics struct {
	StartTime        time.Time                    `json:"start_time"`
	LastCheckTime    time.Time                    `json:"last_check_time"`
	CheckCount       int64                        `json:"check_count"`
	SuccessCount     int64                        `json:"success_count"`
	FailureCount     int64                        `json:"failure_count"`
	ComponentMetrics map[string]*ComponentMetrics `json:"component_metrics"`
}

// ComponentMetrics tracks metrics for individual components.
type ComponentMetrics struct {
	Name           string              `json:"name"`
	CheckCount     int64               `json:"check_count"`
	SuccessCount   int64               `json:"success_count"`
	FailureCount   int64               `json:"failure_count"`
	AverageLatency time.Duration       `json:"average_latency"`
	MinLatency     time.Duration       `json:"min_latency"`
	MaxLatency     time.Duration       `json:"max_latency"`
	LastCheckTime  time.Time           `json:"last_check_time"`
	LastStatus     health.HealthState  `json:"last_status"`
	RecentHistory  []HealthCheckRecord `json:"recent_history,omitempty"`
	HealthTrend    HealthTrend         `json:"health_trend,omitempty"`
}

// HealthCheckRecord represents a single health check execution record.
type HealthCheckRecord struct {
	Timestamp    time.Time          `json:"timestamp"`
	Duration     time.Duration      `json:"duration"`
	Success      bool               `json:"success"`
	Status       health.HealthState `json:"status"`
	ErrorMessage string             `json:"error_message,omitempty"`
}

// HealthTrend represents the health trend analysis.
type HealthTrend struct {
	Direction     TrendDirection `json:"direction"`
	Stability     float64        `json:"stability"`
	RecentSuccess float64        `json:"recent_success"`
	TrendScore    float64        `json:"trend_score"`
}

// TrendDirection represents the direction of health trend.
type TrendDirection string

const (
	TrendImproving TrendDirection = "improving"
	TrendDeclining TrendDirection = "declining"
	TrendStable    TrendDirection = "stable"
)

// HealthReport provides a comprehensive health report with metrics.
type HealthReport struct {
	HealthStatus health.HealthStatus    `json:"health_status"`
	Metrics      HealthMetrics          `json:"metrics"`
	Readiness    health.ReadinessStatus `json:"readiness"`
	BuildInfo    BuildInfo              `json:"build_info"`
	Components   []string               `json:"components"`
}

// Tracker augments an ops health manager with history and metrics.
type Tracker struct {
	manager Manager

	mu      sync.RWMutex
	metrics *HealthMetrics
	history []HealthHistoryEntry
}

// NewTracker creates an ops tracker around a healthhttp manager.
func NewTracker(manager Manager) *Tracker {
	return &Tracker{
		manager: manager,
		metrics: &HealthMetrics{
			StartTime:        time.Now(),
			ComponentMetrics: make(map[string]*ComponentMetrics),
		},
	}
}

// RegisterComponent forwards component registration to the wrapped manager.
func (t *Tracker) RegisterComponent(checker health.ComponentChecker) error {
	if t.manager == nil {
		return errors.New("health manager is nil")
	}
	return t.manager.RegisterComponent(checker)
}

// UnregisterComponent forwards component removal to the wrapped manager.
func (t *Tracker) UnregisterComponent(name string) error {
	if t.manager == nil {
		return errors.New("health manager is nil")
	}
	return t.manager.UnregisterComponent(name)
}

// MarkReady forwards readiness updates to the wrapped manager.
func (t *Tracker) MarkReady() {
	if t.manager != nil {
		t.manager.MarkReady()
	}
}

// MarkNotReady forwards readiness updates to the wrapped manager.
func (t *Tracker) MarkNotReady(reason string) {
	if t.manager != nil {
		t.manager.MarkNotReady(reason)
	}
}

// SetConfig forwards configuration updates to the wrapped manager.
func (t *Tracker) SetConfig(config Config) error {
	if t.manager == nil {
		return errors.New("health manager is nil")
	}
	return t.manager.SetConfig(config)
}

// GetConfig reads the wrapped manager configuration.
func (t *Tracker) GetConfig() Config {
	if t.manager == nil {
		return Config{}
	}
	return t.manager.GetConfig()
}

// Close closes the wrapped manager.
func (t *Tracker) Close() error {
	if t.manager == nil {
		return nil
	}
	return t.manager.Close()
}

// GetComponentHealth forwards component health reads to the wrapped manager.
func (t *Tracker) GetComponentHealth(name string) (*health.ComponentHealth, bool) {
	if t.manager == nil {
		return nil, false
	}
	return t.manager.GetComponentHealth(name)
}

// GetAllHealth forwards aggregate component health reads to the wrapped manager.
func (t *Tracker) GetAllHealth() map[string]*health.ComponentHealth {
	if t.manager == nil {
		return map[string]*health.ComponentHealth{}
	}
	return t.manager.GetAllHealth()
}

// GetOverallHealth forwards aggregate health reads to the wrapped manager.
func (t *Tracker) GetOverallHealth() health.HealthStatus {
	if t.manager == nil {
		return health.HealthStatus{
			Status:    health.StatusUnhealthy,
			Message:   "health manager is nil",
			Timestamp: time.Now(),
		}
	}
	return t.manager.GetOverallHealth()
}

// Readiness forwards readiness reads to the wrapped manager.
func (t *Tracker) Readiness() health.ReadinessStatus {
	if t.manager == nil {
		return health.ReadinessStatus{}
	}
	return t.manager.Readiness()
}

// CheckComponent executes a single component check and records component metrics.
func (t *Tracker) CheckComponent(ctx context.Context, name string) error {
	if t.manager == nil {
		return errors.New("health manager is nil")
	}

	start := time.Now()
	err := t.manager.CheckComponent(ctx, name)
	duration := time.Since(start)

	if component, ok := t.manager.GetComponentHealth(name); ok {
		t.recordComponentCheck(name, component, duration, err)
	}
	return err
}

// CheckAllComponents executes all checks and records aggregate history plus component metrics.
func (t *Tracker) CheckAllComponents(ctx context.Context) health.HealthStatus {
	if t.manager == nil {
		return health.HealthStatus{
			Status:    health.StatusUnhealthy,
			Message:   "health manager is nil",
			Timestamp: time.Now(),
		}
	}

	start := time.Now()
	status := t.manager.CheckAllComponents(ctx)
	allHealth := t.manager.GetAllHealth()
	duration := time.Since(start)

	components := make([]string, 0, len(allHealth))
	for name, component := range allHealth {
		components = append(components, name)
		t.recordComponentCheck(name, component, component.Duration, componentError(component))
	}

	t.mu.Lock()
	t.metrics.LastCheckTime = time.Now()
	t.history = append(t.history, HealthHistoryEntry{
		Timestamp:  status.Timestamp,
		State:      status.Status,
		Message:    status.Message,
		Components: components,
		Duration:   duration,
	})
	t.mu.Unlock()

	return status
}

func componentError(component *health.ComponentHealth) error {
	if component == nil || component.Status == health.StatusHealthy {
		return nil
	}
	if component.Message == "" {
		return errors.New(string(component.Status))
	}
	return errors.New(component.Message)
}

func (t *Tracker) recordComponentCheck(name string, component *health.ComponentHealth, duration time.Duration, err error) {
	if component == nil {
		return
	}

	success := err == nil && component.Status == health.StatusHealthy

	t.mu.Lock()
	defer t.mu.Unlock()

	t.metrics.CheckCount++
	t.metrics.LastCheckTime = time.Now()
	if success {
		t.metrics.SuccessCount++
	} else {
		t.metrics.FailureCount++
	}

	comp, exists := t.metrics.ComponentMetrics[name]
	if !exists {
		comp = &ComponentMetrics{
			Name:          name,
			MinLatency:    duration,
			MaxLatency:    duration,
			LastStatus:    component.Status,
			LastCheckTime: time.Now(),
			RecentHistory: make([]HealthCheckRecord, 0, 10),
		}
		t.metrics.ComponentMetrics[name] = comp
	}

	comp.CheckCount++
	comp.LastCheckTime = time.Now()
	comp.LastStatus = component.Status

	if success {
		comp.SuccessCount++
	} else {
		comp.FailureCount++
	}

	if comp.MinLatency == 0 || duration < comp.MinLatency {
		comp.MinLatency = duration
	}
	if duration > comp.MaxLatency {
		comp.MaxLatency = duration
	}
	if comp.CheckCount == 1 {
		comp.AverageLatency = duration
	} else {
		totalLatency := comp.AverageLatency * time.Duration(comp.CheckCount-1)
		comp.AverageLatency = (totalLatency + duration) / time.Duration(comp.CheckCount)
	}

	record := HealthCheckRecord{
		Timestamp: time.Now(),
		Duration:  duration,
		Success:   success,
		Status:    component.Status,
	}
	if err != nil {
		record.ErrorMessage = err.Error()
	}
	comp.RecentHistory = append(comp.RecentHistory, record)
	if len(comp.RecentHistory) > 10 {
		comp.RecentHistory = comp.RecentHistory[1:]
	}

	t.updateHealthTrend(comp)
}

func (t *Tracker) updateHealthTrend(comp *ComponentMetrics) {
	if len(comp.RecentHistory) < 3 {
		comp.HealthTrend = HealthTrend{
			Direction:     TrendStable,
			Stability:     1.0,
			RecentSuccess: comp.GetRecentSuccessRate(),
			TrendScore:    0.0,
		}
		return
	}

	recentSuccess := comp.GetRecentSuccessRate()
	recentCount := len(comp.RecentHistory)
	halfPoint := recentCount / 2

	var recentGood, olderGood int
	for i, record := range comp.RecentHistory {
		if i >= halfPoint {
			if record.Success {
				recentGood++
			}
			continue
		}
		if record.Success {
			olderGood++
		}
	}

	recentRate := float64(recentGood) / float64(halfPoint)
	olderRate := float64(olderGood) / float64(recentCount-halfPoint)
	trendScore := recentRate - olderRate

	direction := TrendStable
	if trendScore > 0.1 {
		direction = TrendImproving
	} else if trendScore < -0.1 {
		direction = TrendDeclining
	}

	comp.HealthTrend = HealthTrend{
		Direction:     direction,
		Stability:     calculateStability(comp.RecentHistory),
		RecentSuccess: recentSuccess,
		TrendScore:    trendScore,
	}
}

func calculateStability(history []HealthCheckRecord) float64 {
	if len(history) < 2 {
		return 1.0
	}

	successCount := 0
	for _, record := range history {
		if record.Success {
			successCount++
		}
	}

	successRate := float64(successCount) / float64(len(history))
	switch {
	case successRate >= 0.8 || successRate <= 0.2:
		return 0.9
	case successRate >= 0.6 || successRate <= 0.4:
		return 0.7
	default:
		return 0.3
	}
}

// GetRecentSuccessRate calculates the success rate for recent component checks.
func (cm *ComponentMetrics) GetRecentSuccessRate() float64 {
	if len(cm.RecentHistory) == 0 {
		return 0.0
	}

	successCount := 0
	for _, record := range cm.RecentHistory {
		if record.Success {
			successCount++
		}
	}
	return float64(successCount) / float64(len(cm.RecentHistory))
}

// GetMetrics returns a snapshot of current tracked health metrics.
func (t *Tracker) GetMetrics() HealthMetrics {
	t.mu.RLock()
	defer t.mu.RUnlock()

	metricsCopy := HealthMetrics{
		StartTime:        t.metrics.StartTime,
		LastCheckTime:    t.metrics.LastCheckTime,
		CheckCount:       t.metrics.CheckCount,
		SuccessCount:     t.metrics.SuccessCount,
		FailureCount:     t.metrics.FailureCount,
		ComponentMetrics: make(map[string]*ComponentMetrics, len(t.metrics.ComponentMetrics)),
	}
	for name, comp := range t.metrics.ComponentMetrics {
		compCopy := *comp
		compCopy.RecentHistory = append([]HealthCheckRecord(nil), comp.RecentHistory...)
		metricsCopy.ComponentMetrics[name] = &compCopy
	}
	return metricsCopy
}

// GetComponentMetrics returns metrics for a specific component.
func (t *Tracker) GetComponentMetrics(componentName string) (*ComponentMetrics, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	metrics, exists := t.metrics.ComponentMetrics[componentName]
	if !exists {
		return nil, false
	}
	metricsCopy := *metrics
	metricsCopy.RecentHistory = append([]HealthCheckRecord(nil), metrics.RecentHistory...)
	return &metricsCopy, true
}

// GetSuccessRate returns the success rate of checks as a percentage.
func (t *Tracker) GetSuccessRate() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.metrics.CheckCount == 0 {
		return 0.0
	}
	return float64(t.metrics.SuccessCount) / float64(t.metrics.CheckCount) * 100
}

// GetUptime returns tracker uptime.
func (t *Tracker) GetUptime() time.Duration {
	t.mu.RLock()
	start := t.metrics.StartTime
	t.mu.RUnlock()

	uptime := time.Since(start)
	if uptime <= 0 {
		return time.Nanosecond
	}
	return uptime
}

// Reset clears tracker-owned metrics and history.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.metrics = &HealthMetrics{
		StartTime:        time.Now(),
		ComponentMetrics: make(map[string]*ComponentMetrics),
	}
	t.history = nil
}

// GenerateReport generates a comprehensive health report from tracker state.
func (t *Tracker) GenerateReport() HealthReport {
	if t.manager == nil {
		return HealthReport{
			HealthStatus: health.HealthStatus{
				Status:    health.StatusUnhealthy,
				Message:   "health manager is nil",
				Timestamp: time.Now(),
			},
			Metrics:   t.GetMetrics(),
			BuildInfo: GetBuildInfo(),
		}
	}

	allHealth := t.manager.GetAllHealth()
	components := make([]string, 0, len(allHealth))
	for name := range allHealth {
		components = append(components, name)
	}

	return HealthReport{
		HealthStatus: t.manager.GetOverallHealth(),
		Metrics:      t.GetMetrics(),
		Readiness:    t.manager.Readiness(),
		BuildInfo:    GetBuildInfo(),
		Components:   components,
	}
}

// GetHealthHistory returns tracked aggregate health history.
func (t *Tracker) GetHealthHistory() []HealthHistoryEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]HealthHistoryEntry, len(t.history))
	copy(result, t.history)
	return result
}

// QueryHealthHistory returns filtered aggregate health history.
func (t *Tracker) QueryHealthHistory(query HealthHistoryQuery) HealthHistoryQueryResult {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var filtered []HealthHistoryEntry
	for _, entry := range t.history {
		if query.StartTime != nil && entry.Timestamp.Before(*query.StartTime) {
			continue
		}
		if query.EndTime != nil && entry.Timestamp.After(*query.EndTime) {
			continue
		}
		if query.State != nil && entry.State != *query.State {
			continue
		}
		if query.Component != "" {
			found := false
			for _, comp := range entry.Components {
				if comp == query.Component {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		filtered = append(filtered, entry)
	}

	total := len(filtered)
	limit := query.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}

	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}

	entries := make([]HealthHistoryEntry, 0)
	if offset < len(filtered) {
		entries = filtered[offset:end]
	}

	return HealthHistoryQueryResult{
		Entries: entries,
		Total:   total,
		Limit:   limit,
		Offset:  offset,
		HasMore: end < len(filtered),
	}
}

// GetHealthHistoryStats returns statistics about tracked history.
func (t *Tracker) GetHealthHistoryStats() HistoryStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	stateCounts := make(map[string]int)
	if len(t.history) == 0 {
		return HistoryStats{
			EntriesByState: stateCounts,
		}
	}

	for _, entry := range t.history {
		stateCounts[string(entry.State)]++
	}

	oldest := t.history[0]
	newest := t.history[len(t.history)-1]

	return HistoryStats{
		TotalEntries:   len(t.history),
		OldestEntry:    &oldest,
		NewestEntry:    &newest,
		EntriesByState: stateCounts,
		TimeSpan:       newest.Timestamp.Sub(oldest.Timestamp),
	}
}
