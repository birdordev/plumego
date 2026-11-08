package messaging

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/messaging/scheduler"
)

// ChannelState describes the current health of a delivery channel.
type ChannelState string

const (
	ChannelHealthy   ChannelState = "healthy"
	ChannelDegraded  ChannelState = "degraded"
	ChannelUnhealthy ChannelState = "unhealthy"
)

// ChannelStatus reports the health of a single channel.
type ChannelStatus struct {
	Channel     Channel      `json:"channel"`
	Provider    string       `json:"provider"`
	State       ChannelState `json:"state"`
	Latency     string       `json:"latency,omitempty"`
	LastChecked time.Time    `json:"last_checked"`
	Error       string       `json:"error,omitempty"`
}

// ChannelMonitor periodically probes providers to determine channel health.
type ChannelMonitor struct {
	mu       sync.RWMutex
	statuses map[Channel]ChannelStatus
	sms      SMSProvider
	email    EmailProvider
	logger   log.StructuredLogger
}

// NewChannelMonitor creates a monitor for the given providers.
func NewChannelMonitor(sms SMSProvider, email EmailProvider, logger log.StructuredLogger) *ChannelMonitor {
	m := &ChannelMonitor{
		statuses: make(map[Channel]ChannelStatus),
		sms:      sms,
		email:    email,
		logger:   logger,
	}
	// Initialize to healthy.
	if sms != nil {
		m.statuses[ChannelSMS] = ChannelStatus{
			Channel:     ChannelSMS,
			Provider:    sms.Name(),
			State:       ChannelHealthy,
			LastChecked: time.Now(),
		}
	}
	if email != nil {
		m.statuses[ChannelEmail] = ChannelStatus{
			Channel:     ChannelEmail,
			Provider:    email.Name(),
			State:       ChannelHealthy,
			LastChecked: time.Now(),
		}
	}
	return m
}

// RegisterJobs adds the probe job to the scheduler.
func (m *ChannelMonitor) RegisterJobs(sch *scheduler.Scheduler) error {
	if sch == nil {
		return nil
	}
	if _, err := sch.AddCron("messaging.channel-probe", "*/2 * * * *", m.probe,
		scheduler.WithTimeout(30*time.Second),
		scheduler.WithOverlapPolicy(scheduler.SkipIfRunning),
		scheduler.WithGroup("messaging"),
		scheduler.WithTags("health"),
	); err != nil {
		if m.logger != nil {
			m.logger.Error("failed to register channel probe job", log.Fields{
				"job_id": "messaging.channel-probe",
				"error":  err.Error(),
			})
		}
		return fmt.Errorf("messaging: register monitor job messaging.channel-probe: %w", err)
	}
	return nil
}

// Status returns the current status of all channels.
func (m *ChannelMonitor) Status() []ChannelStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ChannelStatus, 0, len(m.statuses))
	for _, s := range m.statuses {
		result = append(result, s)
	}
	return result
}

// ChannelState returns the health of a specific channel.
func (m *ChannelMonitor) ChannelHealth(ch Channel) ChannelState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.statuses[ch]; ok {
		return s.State
	}
	return ChannelUnhealthy
}

// RecordSuccess records a successful send; used by the service after each delivery.
func (m *ChannelMonitor) RecordSuccess(ch Channel, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.statuses[ch]; ok {
		s.State = ChannelHealthy
		s.Latency = latency.String()
		s.Error = ""
		s.LastChecked = time.Now()
		m.statuses[ch] = s
	}
}

// RecordFailure records a failed send.
func (m *ChannelMonitor) RecordFailure(ch Channel, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.statuses[ch]; ok {
		s.State = ChannelDegraded
		s.Error = err.Error()
		s.LastChecked = time.Now()
		m.statuses[ch] = s
	}
}

// probe is the scheduled health check function.
func (m *ChannelMonitor) probe(_ context.Context) error {
	// The probe doesn't send real messages; it just checks that
	// the latest recorded state hasn't been "degraded" for too long.
	// In a full implementation this could call a provider /status endpoint.
	m.mu.Lock()
	defer m.mu.Unlock()

	for ch, s := range m.statuses {
		if s.State == ChannelDegraded && time.Since(s.LastChecked) > 10*time.Minute {
			s.State = ChannelUnhealthy
			m.statuses[ch] = s
			if m.logger != nil {
				m.logger.Warn("channel marked unhealthy", log.Fields{
					"channel":  string(ch),
					"provider": s.Provider,
					"since":    s.LastChecked.Format(time.RFC3339),
				})
			}
		}
	}
	return nil
}
