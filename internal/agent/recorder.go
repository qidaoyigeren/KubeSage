package agent

import (
	"context"
	"time"

	"kubesage/internal/model"
	"kubesage/internal/observability"
)

type stepRecord struct {
	ParentStepID *uint
	Stage        string
	ToolName     string
	Status       string
	Input        interface{}
	Output       interface{}
	Duration     time.Duration
	Reason       string
}

type stepRecorder struct {
	store     Store
	taskID    uint
	traceID   string
	nextIndex int
}

func newStepRecorder(store Store, taskID uint, traceID string) *stepRecorder {
	return &stepRecorder{store: store, taskID: taskID, traceID: traceID}
}

func (r *stepRecorder) record(ctx context.Context, record stepRecord) *model.AgentStep {
	if record.Status == "" {
		record.Status = model.AgentStepStatusSuccess
	}
	r.nextIndex++
	step := &model.AgentStep{
		TaskID:           r.taskID,
		ParentStepID:     record.ParentStepID,
		TraceID:          r.traceID,
		StepIndex:        r.nextIndex,
		Stage:            record.Stage,
		ToolName:         record.ToolName,
		InputJSON:        jsonText(record.Input),
		OutputJSON:       jsonText(record.Output),
		Status:           record.Status,
		DurationMS:       record.Duration.Milliseconds(),
		ReasoningSummary: record.Reason,
		CreatedAt:        time.Now(),
	}
	if r.store != nil {
		_ = r.store.CreateStep(ctx, step)
	}
	observability.IncAgentStepTotal(step.Stage, step.Status, step.ToolName)
	return step
}
