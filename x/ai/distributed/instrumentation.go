package distributed

import (
	"context"
	"time"

	"github.com/birdordev/plumego/x/ai/orchestration"
)

// MetricRecord holds a single metric observation for this package.
type MetricRecord struct {
	Name     string
	Value    float64
	Labels   map[string]string
	Duration time.Duration
	Error    error
}

// MetricRecorder accepts metric records emitted by InstrumentedDistributedEngine.
// metrics.Recorder satisfies this interface via a thin adapter at the call site.
type MetricRecorder interface {
	Record(ctx context.Context, record MetricRecord)
}

type noopRecorder struct{}

func (noopRecorder) Record(context.Context, MetricRecord) {}

// NoopMetricRecorder returns a MetricRecorder that silently discards all records.
func NoopMetricRecorder() MetricRecorder { return noopRecorder{} }

// InstrumentedDistributedEngine wraps DistributedEngine with metrics collection.
type InstrumentedDistributedEngine struct {
	engine    *DistributedEngine
	collector MetricRecorder
}

// NewInstrumentedDistributedEngine creates an instrumented distributed engine.
func NewInstrumentedDistributedEngine(
	engine *DistributedEngine,
	collector MetricRecorder,
) *InstrumentedDistributedEngine {
	return &InstrumentedDistributedEngine{
		engine:    engine,
		collector: collector,
	}
}

// ExecuteAsync executes a workflow asynchronously with metrics.
func (ie *InstrumentedDistributedEngine) ExecuteAsync(
	ctx context.Context,
	workflowID string,
	initialState map[string]any,
	options ExecutionOptions,
) (string, error) {
	start := time.Now()

	executionID, err := ie.engine.ExecuteAsync(ctx, workflowID, initialState, options)

	duration := time.Since(start)

	if ie.collector != nil {
		tags := map[string]string{
			"workflow_id": workflowID,
			"distributed": boolToString(options.Distributed),
			"operation":   "execute_async",
		}

		if err != nil {
			tags["result"] = "error"
		} else {
			tags["result"] = "success"
		}

		ie.collector.Record(ctx, MetricRecord{
			Name:     "workflow_execution_started",
			Value:    1,
			Labels:   tags,
			Duration: duration,
		})
	}

	return executionID, err
}

// ExecuteSync executes a workflow synchronously with metrics.
func (ie *InstrumentedDistributedEngine) ExecuteSync(
	ctx context.Context,
	workflowID string,
	initialState map[string]any,
	options ExecutionOptions,
) ([]*orchestration.AgentResult, error) {
	start := time.Now()

	results, err := ie.engine.ExecuteSync(ctx, workflowID, initialState, options)

	duration := time.Since(start)

	if ie.collector != nil {
		tags := map[string]string{
			"workflow_id": workflowID,
			"distributed": boolToString(options.Distributed),
			"operation":   "execute_sync",
		}

		if err != nil {
			tags["result"] = "error"
		} else {
			tags["result"] = "success"
		}

		ie.collector.Record(ctx, MetricRecord{
			Name:     "workflow_execution_completed",
			Value:    1,
			Labels:   tags,
			Duration: duration,
		})
	}

	return results, err
}

// Resume resumes a paused or failed execution with metrics.
func (ie *InstrumentedDistributedEngine) Resume(ctx context.Context, executionID string) error {
	start := time.Now()

	err := ie.engine.Resume(ctx, executionID)

	duration := time.Since(start)

	if ie.collector != nil {
		tags := map[string]string{
			"execution_id": executionID,
			"operation":    "resume",
		}

		if err != nil {
			tags["result"] = "error"
		} else {
			tags["result"] = "success"
		}

		ie.collector.Record(ctx, MetricRecord{
			Name:     "workflow_resume",
			Value:    1,
			Labels:   tags,
			Duration: duration,
		})
	}

	return err
}

// Pause pauses an execution with metrics.
func (ie *InstrumentedDistributedEngine) Pause(ctx context.Context, executionID string) error {
	start := time.Now()

	err := ie.engine.Pause(ctx, executionID)

	duration := time.Since(start)

	if ie.collector != nil {
		tags := map[string]string{
			"execution_id": executionID,
			"operation":    "pause",
		}

		if err != nil {
			tags["result"] = "error"
		} else {
			tags["result"] = "success"
		}

		ie.collector.Record(ctx, MetricRecord{
			Name:     "workflow_pause",
			Value:    1,
			Labels:   tags,
			Duration: duration,
		})
	}

	return err
}

// GetExecutionStatus retrieves execution status (no metrics needed for read operations).
func (ie *InstrumentedDistributedEngine) GetExecutionStatus(
	ctx context.Context,
	executionID string,
) (*ExecutionSnapshot, error) {
	return ie.engine.GetExecutionStatus(ctx, executionID)
}

// Close closes the instrumented engine.
func (ie *InstrumentedDistributedEngine) Close() error {
	return ie.engine.Close()
}

// boolToString converts boolean to string.
func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
