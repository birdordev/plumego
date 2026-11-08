package instrumentation

import (
	"context"
	"time"

	"github.com/birdordev/plumego/x/ai/metrics"
	"github.com/birdordev/plumego/x/ai/orchestration"
)

// InstrumentedEngine wraps an orchestration Engine with metrics collection.
type InstrumentedEngine struct {
	engine    *orchestration.Engine
	collector metrics.Collector
}

// NewInstrumentedEngine creates a new instrumented orchestration engine.
func NewInstrumentedEngine(engine *orchestration.Engine, collector metrics.Collector) *InstrumentedEngine {
	return &InstrumentedEngine{
		engine:    engine,
		collector: collector,
	}
}

// RegisterWorkflow wraps the engine's RegisterWorkflow method.
func (ie *InstrumentedEngine) RegisterWorkflow(wf *orchestration.Workflow) {
	ie.engine.RegisterWorkflow(wf)

	// Record workflow registration
	tags, _ := metrics.Tags("workflow_id", wf.ID, "workflow_name", wf.Name)
	ie.collector.Counter("ai_workflows_registered_total", 1, tags...)
	ie.collector.Gauge("ai_workflow_steps_count", float64(len(wf.Steps)), tags...)
}

// GetWorkflow wraps the engine's GetWorkflow method.
func (ie *InstrumentedEngine) GetWorkflow(id string) (*orchestration.Workflow, error) {
	start := time.Now()

	wf, err := ie.engine.GetWorkflow(id)

	duration := time.Since(start)
	tags, _ := metrics.Tags("workflow_id", id)
	ie.collector.Timing("ai_workflow_get_duration_seconds", duration, tags...)

	return wf, err
}

// Execute wraps the engine's Execute method with comprehensive metrics.
func (ie *InstrumentedEngine) Execute(ctx context.Context, workflowID string, initialState map[string]any) ([]*orchestration.AgentResult, error) {
	start := time.Now()

	tags, _ := metrics.Tags("workflow_id", workflowID)

	// Increment execution counter
	ie.collector.Counter("ai_workflow_executions_total", 1, tags...)

	// Execute workflow
	results, err := ie.engine.Execute(ctx, workflowID, initialState)

	// Record duration
	duration := time.Since(start)
	ie.collector.Timing("ai_workflow_duration_seconds", duration, tags...)

	// Record status
	status := "success"
	if err != nil {
		status = "failed"
		ie.collector.Counter("ai_workflow_failures_total", 1, tags...)
	}
	statusTags := append(tags, metrics.Tag{Key: "status", Value: status})
	ie.collector.Counter("ai_workflow_by_status_total", 1, statusTags...)

	// Record result metrics
	if err == nil && results != nil {
		ie.collector.Gauge("ai_workflow_steps_executed", float64(len(results)), tags...)

		totalTokens := int64(0)
		for _, result := range results {
			totalTokens += int64(result.TokenUsage.TotalTokens)

			// Record per-agent metrics
			agentTags, _ := metrics.Tags(
				"workflow_id", workflowID,
				"agent_id", result.AgentID,
			)
			ie.collector.Timing("ai_workflow_agent_duration_seconds", result.Duration, agentTags...)
			ie.collector.Histogram("ai_workflow_agent_tokens", float64(result.TokenUsage.TotalTokens), agentTags...)
		}

		ie.collector.Histogram("ai_workflow_total_tokens", float64(totalTokens), tags...)
	}

	return results, err
}

// InstrumentedStep wraps a workflow Step with metrics.
type InstrumentedStep struct {
	step      orchestration.Step
	collector metrics.Collector
}

// NewInstrumentedStep creates an instrumented step.
func NewInstrumentedStep(step orchestration.Step, collector metrics.Collector) *InstrumentedStep {
	return &InstrumentedStep{
		step:      step,
		collector: collector,
	}
}

// Execute implements orchestration.Step
func (is *InstrumentedStep) Execute(ctx context.Context, wf *orchestration.Workflow) (*orchestration.AgentResult, error) {
	start := time.Now()

	tags, _ := metrics.Tags(
		"workflow_id", wf.ID,
		"step_name", is.step.Name(),
	)

	// Increment step execution counter
	is.collector.Counter("ai_workflow_step_executions_total", 1, tags...)

	// Execute step
	result, err := is.step.Execute(ctx, wf)

	// Record duration
	duration := time.Since(start)
	is.collector.Timing("ai_workflow_step_duration_seconds", duration, tags...)

	// Record status
	status := "success"
	if err != nil {
		status = "failed"
		is.collector.Counter("ai_workflow_step_failures_total", 1, tags...)
	}
	statusTags := append(tags, metrics.Tag{Key: "status", Value: status})
	is.collector.Counter("ai_workflow_step_by_status_total", 1, statusTags...)

	return result, err
}

// Name implements orchestration.Step
func (is *InstrumentedStep) Name() string {
	return is.step.Name()
}
