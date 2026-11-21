package healthhttp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/birdordev/plumego/health"
)

var (
	errManagerClosed            = errors.New("manager is closed")
	errComponentCheckIncomplete = errors.New("component check did not complete")
)

// Config holds ops-owned health check execution policy.
type Config struct {
	Enabled    bool          `json:"enabled"`
	Timeout    time.Duration `json:"timeout"`
	RetryCount int           `json:"retry_count"`
	RetryDelay time.Duration `json:"retry_delay"`
	// HealthCheckInterval 保留给调用方配置周期调度。
	// Manager 在调用时执行检查。
	HealthCheckInterval time.Duration `json:"health_check_interval"`
}

// ComponentRegistry handles component registration and removal.
type ComponentRegistry interface {
	RegisterComponent(checker health.ComponentChecker) error
	UnregisterComponent(name string) error
}

// Checker executes health checks and reads component status.
type Checker interface {
	CheckComponent(ctx context.Context, name string) error
	CheckAllComponents(ctx context.Context) health.HealthStatus
	GetComponentHealth(name string) (*health.ComponentHealth, bool)
	GetAllHealth() map[string]*health.ComponentHealth
	GetOverallHealth() health.HealthStatus
	Readiness() health.ReadinessStatus
}

// Manager owns ops-facing health check orchestration.
type Manager interface {
	ComponentRegistry
	Checker
	MarkReady()
	MarkNotReady(reason string)
	SetConfig(config Config) error
	GetConfig() Config
	Close() error
}

type manager struct {
	mu                sync.RWMutex
	components        map[string]health.ComponentChecker
	health            map[string]*health.ComponentHealth
	checkVersions     map[string]uint64
	aggregateVersion  uint64
	config            Config
	closed            bool
	lastCheckDuration time.Duration

	readinessMu sync.RWMutex
	readiness   health.ReadinessStatus
}

// NewManager creates an ops health manager.
func NewManager(config Config) (Manager, error) {
	if err := validateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	now := time.Now()
	return &manager{
		components:    make(map[string]health.ComponentChecker),
		health:        make(map[string]*health.ComponentHealth),
		checkVersions: make(map[string]uint64),
		config:        config,
		readiness: health.ReadinessStatus{
			Ready:     false,
			Reason:    "starting",
			Timestamp: now,
		},
	}, nil
}

func validateConfig(config Config) error {
	if config.Timeout < 0 {
		return errors.New("timeout cannot be negative")
	}
	if config.RetryCount < 0 {
		return errors.New("retry count cannot be negative")
	}
	if config.RetryCount == math.MaxInt {
		return errors.New("retry count exceeds maximum supported attempts")
	}
	if config.RetryDelay < 0 {
		return errors.New("retry delay cannot be negative")
	}
	return nil
}

func (m *manager) MarkReady() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.aggregateVersion++
	m.readinessMu.Lock()
	defer m.readinessMu.Unlock()
	m.readiness = health.ReadinessStatus{
		Ready:     true,
		Timestamp: time.Now(),
	}
}

func (m *manager) MarkNotReady(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.aggregateVersion++
	m.readinessMu.Lock()
	defer m.readinessMu.Unlock()
	m.readiness = health.ReadinessStatus{
		Ready:     false,
		Reason:    reason,
		Timestamp: time.Now(),
	}
}

func (m *manager) Readiness() health.ReadinessStatus {
	m.readinessMu.RLock()
	defer m.readinessMu.RUnlock()
	return m.readiness.Clone()
}

func (m *manager) RegisterComponent(checker health.ComponentChecker) error {
	if checker == nil {
		return errors.New("checker cannot be nil")
	}

	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return errManagerClosed
	}

	name := checker.Name()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errManagerClosed
	}
	if name == "" {
		return errors.New("component name cannot be empty")
	}

	m.components[name] = checker
	m.health[name] = &health.ComponentHealth{
		HealthStatus: health.HealthStatus{
			Status:    health.StatusUnhealthy,
			Timestamp: time.Now(),
			Message:   "component registered",
		},
		Enabled: true,
	}
	return nil
}

func (m *manager) UnregisterComponent(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return errManagerClosed
	}
	if _, exists := m.components[name]; !exists {
		return fmt.Errorf("component %s not found", name)
	}

	delete(m.components, name)
	delete(m.health, name)
	delete(m.checkVersions, name)
	return nil
}

func (m *manager) GetComponentHealth(name string) (*health.ComponentHealth, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	component, exists := m.health[name]
	if !exists {
		return nil, false
	}
	return copyComponentHealth(component), true
}

func (m *manager) GetAllHealth() map[string]*health.ComponentHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*health.ComponentHealth, len(m.health))
	for name, component := range m.health {
		result[name] = copyComponentHealth(component)
	}
	return result
}

func (m *manager) CheckComponent(ctx context.Context, name string) error {
	var result componentCheckResult
	return m.checkComponent(ctx, name, &result)
}

func (m *manager) checkComponent(ctx context.Context, name string, result *componentCheckResult) error {
	if ctx == nil {
		ctx = context.Background()
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errManagerClosed
	}
	checker, exists := m.components[name]
	expectedHealth := m.health[name]
	config := m.config
	var checkVersion uint64
	if exists {
		if m.checkVersions == nil {
			m.checkVersions = make(map[string]uint64)
		}
		m.checkVersions[name]++
		checkVersion = m.checkVersions[name]
	}
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("component %s not found", name)
	}

	checkCtx, cancel := withCheckTimeout(ctx, config.Timeout)
	defer cancel()

	start := time.Now()
	attempts, err := runCheckWithRetry(checkCtx, checker, config)
	duration := time.Since(start)
	result.attempted = attempts > 0
	result.duration = duration
	result.failed = err != nil

	timestamp := time.Now()
	status := health.StatusHealthy
	message := "component is healthy"
	if err != nil {
		status = health.StatusUnhealthy
		message = "component check failed"
	}

	m.mu.Lock()
	component, healthExists := m.health[name]
	if m.closed {
		m.mu.Unlock()
		return errManagerClosed
	}
	if !healthExists {
		m.mu.Unlock()
		return fmt.Errorf("health status for component %s not found", name)
	}
	if component != expectedHealth || m.checkVersions[name] != checkVersion {
		m.mu.Unlock()
		return errors.New("component check was superseded")
	}

	component.Status = status
	component.Message = message
	component.Timestamp = timestamp
	component.Duration = duration
	if component.Details == nil {
		component.Details = make(map[string]any)
	}
	component.Details["last_check"] = timestamp
	component.Details["check_duration"] = duration.String()
	component.Details["check_attempts"] = attempts
	m.lastCheckDuration = duration
	m.mu.Unlock()

	return err
}

func (m *manager) CheckAllComponents(ctx context.Context) health.HealthStatus {
	status, _, _ := m.checkBatch(ctx)
	return status
}

func (m *manager) checkBatch(ctx context.Context) (health.HealthStatus, []string, []componentCheckResult) {
	if ctx == nil {
		ctx = context.Background()
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return health.HealthStatus{
			Status:    health.StatusUnhealthy,
			Message:   errManagerClosed.Error(),
			Timestamp: time.Now(),
		}, nil, nil
	}
	m.aggregateVersion++
	version := m.aggregateVersion
	components := make([]string, 0, len(m.components))
	for name := range m.components {
		components = append(components, name)
	}
	m.mu.Unlock()

	sort.Strings(components)

	start := time.Now()
	results := m.checkAggregateComponents(ctx, components)
	var failedComponents []string
	for i, result := range results {
		if !result.complete || result.err != nil {
			failedComponents = append(failedComponents, components[i])
		}
	}
	duration := time.Since(start)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return health.HealthStatus{Status: health.StatusUnhealthy, Message: errManagerClosed.Error(), Timestamp: time.Now()}, components, results
	}
	if version != m.aggregateVersion {
		return health.HealthStatus{Status: health.StatusUnhealthy, Message: "aggregate check was superseded", Timestamp: time.Now()}, components, results
	}
	status, message := calculateOverallStatus(m.health)
	if len(components) == 0 && len(m.health) == 0 {
		message = "no components registered"
	}
	if len(failedComponents) != 0 {
		if status.IsReady() {
			message = "component checks did not all succeed"
		}
		status = health.StatusUnhealthy
	}
	if err := ctx.Err(); err != nil {
		status = health.StatusUnhealthy
		message = err.Error()
	}
	timestamp := time.Now()
	readiness := health.ReadinessStatus{
		Timestamp:  timestamp,
		Components: make(map[string]bool, len(m.health)),
	}
	if status.IsReady() {
		readiness.Ready = true
	} else {
		readiness.Ready = false
		readiness.Reason = fmt.Sprintf("components failed: %v", failedComponents)
	}
	for name, component := range m.health {
		readiness.Components[name] = component.Status.IsReady()
	}
	for i, result := range results {
		if !result.complete || result.err == errComponentCheckIncomplete {
			if name := components[i]; m.health[name] != nil {
				readiness.Components[name] = false
			}
		}
	}

	m.readinessMu.Lock()
	m.readiness = readiness
	m.readinessMu.Unlock()

	m.lastCheckDuration = duration

	return health.HealthStatus{
		Status:       status,
		Message:      message,
		Timestamp:    timestamp,
		Details:      map[string]any{},
		Duration:     duration,
		Dependencies: components,
	}, components, results
}

func optimalConcurrency(componentCount int) int {
	switch {
	case componentCount <= 5:
		return componentCount
	case componentCount <= 20:
		return 5
	default:
		return 8
	}
}

func (m *manager) GetOverallHealth() health.HealthStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return health.HealthStatus{Status: health.StatusUnhealthy, Message: errManagerClosed.Error(), Timestamp: time.Now()}
	}

	status, message := calculateOverallStatus(m.health)
	return health.HealthStatus{
		Status:       status,
		Message:      message,
		Timestamp:    time.Now(),
		Details:      map[string]any{},
		Duration:     m.lastCheckDuration,
		Dependencies: m.componentNames(),
	}
}

func (m *manager) componentNames() []string {
	names := make([]string, 0, len(m.components))
	for name := range m.components {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *manager) SetConfig(config Config) error {
	if err := validateConfig(config); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errManagerClosed
	}
	m.config = config
	return nil
}

func (m *manager) GetConfig() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config
}

func (m *manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	m.readinessMu.Lock()
	m.readiness = health.ReadinessStatus{Ready: false, Reason: errManagerClosed.Error(), Timestamp: time.Now()}
	m.readinessMu.Unlock()
	return nil
}

func runCheckWithRetry(ctx context.Context, checker health.ComponentChecker, config Config) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	maxAttempts := config.RetryCount + 1

	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return attempt - 1, err
		}
		err = checker.Check(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return attempt, ctxErr
		}
		if err == nil {
			return attempt, nil
		}
		if attempt >= maxAttempts {
			return attempt, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return attempt, ctxErr
		}
		if !sleepWithContext(ctx, config.RetryDelay) {
			return attempt, ctx.Err()
		}
	}
	return maxAttempts, err
}

func sleepWithContext(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	if ctx == nil {
		time.Sleep(delay)
		return true
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type componentCheckResult struct {
	complete  bool
	attempted bool
	duration  time.Duration
	failed    bool
	err       error
}

func (m *manager) checkAggregateComponents(ctx context.Context, names []string) []componentCheckResult {
	results := make([]componentCheckResult, len(names))
	var next atomic.Int64
	var wg sync.WaitGroup

	var worker func()
	worker = func() {
		current := -1
		var started time.Time
		defer func() {
			_ = recover()
			if current >= 0 {
				// panic 和 Goexit 都会执行此 defer。
				// 仅在还有未领取的检查时补充 worker。
				results[current] = componentCheckResult{
					complete: true, attempted: true, duration: time.Since(started),
					failed: true, err: errComponentCheckIncomplete,
				}
				if next.Load() < int64(len(names)) {
					wg.Go(worker)
				}
			}
		}()
		for ctx.Err() == nil {
			index := next.Add(1) - 1
			if index >= int64(len(names)) {
				return
			}
			current = int(index)
			started = time.Now()
			result := &results[current]
			result.err = m.checkComponent(ctx, names[current], result)
			result.complete = true
			current = -1
		}
	}
	for range optimalConcurrency(len(names)) {
		wg.Go(worker)
	}
	wg.Wait()
	return results
}

func copyComponentHealth(src *health.ComponentHealth) *health.ComponentHealth {
	dst := src.Clone()
	return &dst
}

func calculateOverallStatus(healths map[string]*health.ComponentHealth) (health.HealthState, string) {
	allHealthy := true
	var messages []string

	for _, component := range healths {
		if component.Status != health.StatusHealthy {
			allHealthy = false
			if component.Message != "" {
				messages = append(messages, fmt.Sprintf("%s: %s", component.Status, component.Message))
			}
		}
	}

	if allHealthy {
		return health.StatusHealthy, ""
	}

	status := health.StatusDegraded
	for _, component := range healths {
		if !component.Status.IsReady() {
			status = health.StatusUnhealthy
			break
		}
	}

	sort.Strings(messages)
	message := ""
	if len(messages) > 0 {
		message = fmt.Sprintf("Issues detected: %v", messages)
	}
	return status, message
}
