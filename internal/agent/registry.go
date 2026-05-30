package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
	"kubesage/internal/observability"
)

type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry creates an empty registry for structured agent tools.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]Tool{}}
}

// Register adds a tool by name and rejects duplicate registrations.
func (r *ToolRegistry) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("tool is nil")
	}
	meta := tool.Metadata()
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		return fmt.Errorf("tool name is empty")
	}
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %s already registered", name)
	}
	r.tools[name] = tool
	return nil
}

// Get returns a registered tool by name.
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	tool, ok := r.tools[name]
	return tool, ok
}

// Metadata returns every registered tool metadata record.
func (r *ToolRegistry) Metadata() []ToolMetadata {
	if r == nil {
		return nil
	}
	result := make([]ToolMetadata, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool.Metadata())
	}
	return result
}

type simpleTool struct {
	meta ToolMetadata
	fn   func(context.Context, map[string]interface{}, *ToolState) ToolResult
}

func (t simpleTool) Metadata() ToolMetadata { return t.meta }

func (t simpleTool) Execute(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
	start := time.Now()
	result := t.fn(ctx, input, state)
	result.ToolName = t.meta.Name
	result.DurationMS = time.Since(start).Milliseconds()
	if result.Observation == "" && result.Error != "" {
		result.Observation = result.Error
	}
	return result
}

type RegistryOptions struct {
	Snapshot    SnapshotFunc
	Retriever   Retriever
	Policy      *RemediationPolicy
	ToolTimeout time.Duration
}

// NewDefaultRegistry wires the MVP read-only tools used by the agent runtime.
func NewDefaultRegistry(opts RegistryOptions) (*ToolRegistry, error) {
	registry := NewToolRegistry()
	tools := []Tool{
		newSnapshotTool("k8s.get_pod", "Load pod snapshot and cache diagnostic context.", true, opts.Snapshot, podObservation),
		newSnapshotTool("k8s.get_events", "Read pod Events from cached Kubernetes snapshot.", false, opts.Snapshot, eventsObservation),
		newSnapshotTool("k8s.get_previous_logs", "Read previous/current pod logs from cached Kubernetes snapshot.", false, opts.Snapshot, logsObservation),
		newSnapshotTool("k8s.get_topology", "Read workload, service, endpoint, and node topology.", false, opts.Snapshot, topologyObservation),
		newSnapshotTool("k8s.get_pvc_status", "Read PVC status referenced by the pod.", false, opts.Snapshot, pvcObservation),
		newRunbookSearchTool(opts.Retriever),
		newPrometheusQueryTool(),
		newLokiQueryTool(),
		newRemediationGenerateTool(opts.Policy),
		newRemediationDryRunTool(opts.Policy),
	}
	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func newSnapshotTool(name, description string, critical bool, snapshot SnapshotFunc, observe func(*diagnostic.DiagnosticContext) ToolResult) Tool {
	return simpleTool{
		meta: ToolMetadata{
			Name:        name,
			Description: description,
			InputSchema: map[string]string{"namespace": "string", "pod_name": "string"},
			RiskLevel:   "low",
			ReadOnly:    true,
			Timeout:     10 * time.Second,
			Critical:    critical,
		},
		fn: func(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
			if state == nil {
				return failedTool(name, "tool state is nil")
			}
			if state.DiagnosticContext == nil {
				if snapshot == nil {
					return failedTool(name, "snapshot collector is not configured")
				}
				diagCtx, err := snapshot(ctx, state.Goal)
				if err != nil {
					return ToolResult{ToolName: name, Success: false, Error: err.Error(), Observation: "snapshot collection failed"}
				}
				state.DiagnosticContext = diagCtx
			}
			return observe(state.DiagnosticContext)
		},
	}
}

func podObservation(ctx *diagnostic.DiagnosticContext) ToolResult {
	if ctx == nil || ctx.Pod == nil {
		return failedTool("k8s.get_pod", "pod snapshot is empty")
	}
	return ToolResult{
		Success:     true,
		Observation: fmt.Sprintf("pod %s/%s phase=%s containers=%d", ctx.Namespace, ctx.PodName, ctx.Pod.Status.Phase, len(ctx.Pod.Spec.Containers)),
		Data:        ctx.Pod,
		EvidenceRecords: []diagnostic.EvidenceRecord{{
			SourceType: "k8s_pod",
			Title:      "Pod snapshot loaded",
			Content:    fmt.Sprintf("namespace=%s pod=%s phase=%s node=%s", ctx.Namespace, ctx.PodName, ctx.Pod.Status.Phase, ctx.Pod.Spec.NodeName),
			Severity:   "info",
			Raw:        ctx.Pod,
			Timestamp:  time.Now(),
		}},
	}
}

func eventsObservation(ctx *diagnostic.DiagnosticContext) ToolResult {
	if ctx == nil {
		return failedTool("k8s.get_events", "diagnostic context is empty")
	}
	records := make([]diagnostic.EvidenceRecord, 0, len(ctx.Events))
	for _, event := range ctx.Events {
		records = append(records, diagnostic.EvidenceRecord{
			SourceType: "k8s_event",
			Title:      event.Reason,
			Content:    event.Message,
			Severity:   eventSeverity(event.Reason, event.Message),
			Raw:        event,
			Timestamp:  event.LastTimestamp.Time,
		})
	}
	return ToolResult{Success: true, Observation: fmt.Sprintf("collected %d pod events", len(ctx.Events)), EvidenceRecords: records, Data: ctx.Events}
}

func logsObservation(ctx *diagnostic.DiagnosticContext) ToolResult {
	if ctx == nil {
		return failedTool("k8s.get_previous_logs", "diagnostic context is empty")
	}
	records := make([]diagnostic.EvidenceRecord, 0, len(ctx.Logs))
	for _, logs := range ctx.Logs {
		content := logs.Previous
		if content == "" {
			content = logs.Current
		}
		if content == "" {
			content = logs.Loki
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		records = append(records, diagnostic.EvidenceRecord{
			SourceType: "k8s_log",
			Title:      "Container logs",
			Content:    fmt.Sprintf("container=%s\n%s", logs.ContainerName, trimText(content, 4000)),
			Severity:   "info",
			Raw:        logs,
			Timestamp:  time.Now(),
		})
	}
	return ToolResult{Success: true, Observation: fmt.Sprintf("collected logs for %d containers", len(ctx.Logs)), EvidenceRecords: records, Data: ctx.Logs}
}

func topologyObservation(ctx *diagnostic.DiagnosticContext) ToolResult {
	if ctx == nil || ctx.Topology == nil {
		return ToolResult{Success: true, Observation: "topology unavailable", Data: nil}
	}
	return ToolResult{Success: true, Observation: "topology snapshot available", Data: ctx.Topology}
}

func pvcObservation(ctx *diagnostic.DiagnosticContext) ToolResult {
	if ctx == nil {
		return failedTool("k8s.get_pvc_status", "diagnostic context is empty")
	}
	records := make([]diagnostic.EvidenceRecord, 0, len(ctx.PVCs))
	for _, pvc := range ctx.PVCs {
		severity := "info"
		if pvc.Phase != "Bound" {
			severity = "warning"
		}
		records = append(records, diagnostic.EvidenceRecord{
			SourceType: "k8s_pvc",
			Title:      "PVC binding status",
			Content:    fmt.Sprintf("pvc=%s phase=%s storageClass=%s volumeName=%s capacity=%s", pvc.Name, pvc.Phase, pvc.StorageClass, pvc.VolumeName, pvc.Capacity),
			Severity:   severity,
			Raw:        pvc,
			Timestamp:  time.Now(),
		})
	}
	return ToolResult{Success: true, Observation: fmt.Sprintf("collected %d pvc statuses", len(ctx.PVCs)), EvidenceRecords: records, Data: ctx.PVCs}
}

func newRunbookSearchTool(retriever Retriever) Tool {
	return simpleTool{
		meta: ToolMetadata{
			Name:        "runbook.search",
			Description: "Search runbook guidance for planner hints.",
			InputSchema: map[string]string{"fault_type": "string", "query": "string"},
			RiskLevel:   "low",
			ReadOnly:    true,
			Timeout:     10 * time.Second,
		},
		fn: func(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
			if retriever == nil {
				return ToolResult{Success: true, Observation: "runbook retriever is not configured"}
			}
			faultType := stringInput(input, "fault_type")
			query := stringInput(input, "query")
			if faultType == "" && state != nil {
				faultType = state.Goal.ExpectedFault
			}
			hits, err := retriever.Retrieve(ctx, faultType, query, 3)
			if err != nil {
				return failedTool("runbook.search", err.Error())
			}
			if state != nil {
				state.RunbookHits = hits
			}
			records := make([]diagnostic.EvidenceRecord, 0, len(hits))
			for _, hit := range hits {
				records = append(records, diagnostic.EvidenceRecord{
					SourceType: "runbook",
					Title:      hit.Title,
					Content:    hit.Content,
					Severity:   "info",
					Raw:        hit,
					Timestamp:  time.Now(),
				})
			}
			return ToolResult{Success: true, Observation: fmt.Sprintf("runbook hits=%d", len(hits)), EvidenceRecords: records, Data: hits}
		},
	}
}

func newPrometheusQueryTool() Tool {
	return simpleTool{
		meta: ToolMetadata{
			Name:        "prometheus.query_range",
			Description: "Record Prometheus metric query intent; analyzers perform concrete query enrichment.",
			InputSchema: map[string]string{"query": "string", "start": "RFC3339", "end": "RFC3339"},
			RiskLevel:   "low",
			ReadOnly:    true,
			Timeout:     10 * time.Second,
		},
		fn: func(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
			_ = ctx
			query := stringInput(input, "query")
			if query == "" {
				query = "container_memory_working_set_bytes or rule-selected metric"
			}
			enabled := state != nil && state.Goal.IncludeMetrics
			return ToolResult{Success: true, Observation: fmt.Sprintf("prometheus query planned enabled=%t query=%s", enabled, query), Data: input}
		},
	}
}

func newLokiQueryTool() Tool {
	return simpleTool{
		meta: ToolMetadata{
			Name:        "loki.query_logs",
			Description: "Read Loki-enriched logs already collected in the snapshot when configured.",
			InputSchema: map[string]string{"namespace": "string", "pod_name": "string", "container_name": "string"},
			RiskLevel:   "low",
			ReadOnly:    true,
			Timeout:     10 * time.Second,
		},
		fn: func(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
			_ = ctx
			_ = input
			if state == nil || state.DiagnosticContext == nil {
				return ToolResult{Success: true, Observation: "loki logs unavailable before snapshot"}
			}
			count := 0
			for _, logs := range state.DiagnosticContext.Logs {
				if strings.TrimSpace(logs.Loki) != "" {
					count++
				}
			}
			return ToolResult{Success: true, Observation: fmt.Sprintf("loki log windows available for %d containers", count), Data: state.DiagnosticContext.Logs}
		},
	}
}

func newRemediationGenerateTool(policy *RemediationPolicy) Tool {
	return simpleTool{
		meta: ToolMetadata{
			Name:        "remediation.generate_actions",
			Description: "Generate and policy-sanitize remediation action proposals.",
			InputSchema: map[string]string{"fault_type": "string"},
			RiskLevel:   "medium",
			ReadOnly:    true,
			Timeout:     10 * time.Second,
		},
		fn: func(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
			_ = ctx
			_ = input
			if state == nil || state.DiagnosticContext == nil || state.Report == nil {
				return failedTool("remediation.generate_actions", "diagnostic report is not ready")
			}
			actions := diagnostic.GenerateRemediationActions(state.DiagnosticContext, state.Report)
			if policy == nil {
				policy = NewRemediationPolicy(true)
			}
			actions, executions := policy.SanitizeActions(state.TaskID, actions)
			state.RemediationActions = actions
			state.Executions = executions
			state.Report.RemediationActions = actions
			return ToolResult{Success: true, Observation: fmt.Sprintf("generated %d remediation actions", len(actions)), Data: actions}
		},
	}
}

func newRemediationDryRunTool(policy *RemediationPolicy) Tool {
	return simpleTool{
		meta: ToolMetadata{
			Name:        "remediation.dry_run_patch",
			Description: "Validate and preview a dry-run remediation command without shell execution.",
			InputSchema: map[string]string{"command_preview": "string", "risk_level": "string"},
			RiskLevel:   "medium",
			ReadOnly:    true,
			Timeout:     10 * time.Second,
		},
		fn: func(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
			_ = ctx
			command := stringInput(input, "command_preview")
			risk := stringInput(input, "risk_level")
			if policy == nil {
				policy = NewRemediationPolicy(true)
			}
			taskID := uint(0)
			if state != nil {
				taskID = state.TaskID
			}
			execution := policy.ValidateDryRunPreview(taskID, command, risk)
			if state != nil {
				state.Executions = append(state.Executions, execution)
			}
			return ToolResult{
				Success:     execution.Status == model.RemediationExecutionStatusDryRunSuccess || execution.Status == model.RemediationExecutionStatusDryRunPending,
				Observation: execution.DryRunOutput,
				Error:       errorForExecution(execution),
				Data:        execution,
			}
		},
	}
}

func failedTool(name, err string) ToolResult {
	return ToolResult{ToolName: name, Success: false, Error: err, Observation: err}
}

func eventSeverity(reason, message string) string {
	text := strings.ToLower(reason + " " + message)
	if strings.Contains(text, "failed") || strings.Contains(text, "backoff") || strings.Contains(text, "unhealthy") || strings.Contains(text, "oom") {
		return "warning"
	}
	return "info"
}

func stringInput(input map[string]interface{}, key string) string {
	if input == nil {
		return ""
	}
	if value, ok := input[key].(string); ok {
		return value
	}
	return ""
}

func trimText(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[len(text)-limit:]
}

func observeToolMetrics(result ToolResult) {
	status := model.AgentStepStatusSuccess
	if !result.Success {
		status = model.AgentStepStatusFailed
	}
	observability.IncAgentStepTotal(StageToolCall, status, result.ToolName)
	observability.ObserveAgentToolDuration(result.ToolName, time.Duration(result.DurationMS)*time.Millisecond)
}
