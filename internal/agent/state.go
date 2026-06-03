package agent

import (
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"

	corev1 "k8s.io/api/core/v1"
)

func newToolState(taskID uint, goal Goal, tools []ToolMetadata) *ToolState {
	return &ToolState{
		TaskID:         taskID,
		Goal:           goal,
		AvailableTools: append([]ToolMetadata(nil), tools...),
	}
}

func (s *ToolState) RecordToolResult(result ToolResult) {
	if s == nil {
		return
	}
	refs := make([]string, 0, len(result.EvidenceRecords))
	for i, record := range result.EvidenceRecords {
		refs = append(refs, evidenceRef(record, i))
	}
	observation := ObservationRecord{
		ToolName:        result.ToolName,
		Success:         result.Success,
		Observation:     result.Observation,
		Warnings:        append([]string(nil), result.Warnings...),
		MissingEvidence: append([]string(nil), result.MissingEvidence...),
		EvidenceRefs:    refs,
		Error:           result.Error,
		Timestamp:       time.Now(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Observations = append(s.Observations, observation)
	s.EvidenceRecords = append(s.EvidenceRecords, result.EvidenceRecords...)
	s.applyDeltaLocked(result.StateDelta)
}

func (s *ToolState) SnapshotForTool() *ToolState {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return &ToolState{
		TaskID:             s.TaskID,
		Goal:               s.Goal,
		AvailableTools:     append([]ToolMetadata(nil), s.AvailableTools...),
		Observations:       append([]ObservationRecord(nil), s.Observations...),
		EvidenceRecords:    append([]diagnostic.EvidenceRecord(nil), s.EvidenceRecords...),
		DiagnosticContext:  cloneDiagnosticContext(s.DiagnosticContext),
		Report:             cloneReport(s.Report),
		RunbookHits:        append([]RunbookHit(nil), s.RunbookHits...),
		RemediationActions: append([]diagnostic.RemediationAction(nil), s.RemediationActions...),
		Executions:         append([]model.RemediationExecution(nil), s.Executions...),
		CompletedTools:     cloneCompletedTools(s.CompletedTools),
	}
}

// ReadOnly returns an immutable snapshot suitable for passing to tool function
// closures. Tools should use the getter methods on ReadOnlyToolState and express
// state changes via ToolResult.StateDelta.
func (s *ToolState) ReadOnly() *ReadOnlyToolState {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return &ReadOnlyToolState{
		TaskID:            s.TaskID,
		Goal:              s.Goal,
		DiagnosticContext: s.DiagnosticContext,
		Report:            s.Report,
		RunbookHits:       append([]RunbookHit(nil), s.RunbookHits...),
		CompletedTools:    cloneCompletedTools(s.CompletedTools),
	}
}

func (s *ToolState) ObservationSnapshot() []ObservationRecord {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ObservationRecord(nil), s.Observations...)
}

func (s *ToolState) EvidenceSnapshot() []diagnostic.EvidenceRecord {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]diagnostic.EvidenceRecord(nil), s.EvidenceRecords...)
}

func (s *ToolState) ToolMetadataSnapshot() []ToolMetadata {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ToolMetadata(nil), s.AvailableTools...)
}

func (s *ToolState) applyDeltaLocked(delta *ToolStateDelta) {
	if delta == nil {
		return
	}
	if delta.Goal != nil {
		s.Goal = *delta.Goal
	}
	if delta.DiagnosticContext != nil {
		s.DiagnosticContext = cloneDiagnosticContext(delta.DiagnosticContext)
	}
	if delta.Report != nil {
		s.Report = cloneReport(delta.Report)
	}
	if delta.SetRunbookHits {
		s.RunbookHits = append([]RunbookHit(nil), delta.RunbookHits...)
	}
	if delta.SetRemediationActions {
		s.RemediationActions = append([]diagnostic.RemediationAction(nil), delta.RemediationActions...)
		if s.Report != nil {
			s.Report.RemediationActions = append([]diagnostic.RemediationAction(nil), delta.RemediationActions...)
		}
	}
	if delta.SetExecutions {
		s.Executions = append([]model.RemediationExecution(nil), delta.Executions...)
	}
	if len(delta.AppendExecutions) > 0 {
		s.Executions = append(s.Executions, delta.AppendExecutions...)
	}
}

func cloneDiagnosticContext(ctx *diagnostic.DiagnosticContext) *diagnostic.DiagnosticContext {
	if ctx == nil {
		return nil
	}
	clone := *ctx
	if ctx.Pod != nil {
		clone.Pod = ctx.Pod.DeepCopy()
	}
	clone.Events = append([]corev1.Event(nil), ctx.Events...)
	clone.Logs = append([]diagnostic.ContainerLogs(nil), ctx.Logs...)
	clone.PVCs = append([]diagnostic.PVCBrief(nil), ctx.PVCs...)
	clone.MetricTrends = append([]diagnostic.MetricTrend(nil), ctx.MetricTrends...)
	clone.NodeSnapshots = append([]diagnostic.NodeSnapshot(nil), ctx.NodeSnapshots...)
	clone.RunbookHits = append([]diagnostic.RunbookHit(nil), ctx.RunbookHits...)
	return &clone
}

func cloneReport(report *diagnostic.Report) *diagnostic.Report {
	if report == nil {
		return nil
	}
	clone := *report
	clone.Evidences = append([]diagnostic.EvidenceRecord(nil), report.Evidences...)
	clone.SuggestedActions = append([]string(nil), report.SuggestedActions...)
	clone.RemediationActions = append([]diagnostic.RemediationAction(nil), report.RemediationActions...)
	clone.ContributingFactors = append([]diagnostic.RootCauseFactor(nil), report.ContributingFactors...)
	clone.ResidualRisks = append([]string(nil), report.ResidualRisks...)
	if report.PrimaryRootCause != nil {
		primary := *report.PrimaryRootCause
		primary.EvidenceRefs = append([]string(nil), report.PrimaryRootCause.EvidenceRefs...)
		clone.PrimaryRootCause = &primary
	}
	return &clone
}

func cloneCompletedTools(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
