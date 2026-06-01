package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/loki"
	"kubesage/internal/model"
	"kubesage/internal/observability"
	"kubesage/internal/prometheus"
)

type ToolRegistry struct {
	tools   map[string]Tool
	aliases map[string]string
}

// NewToolRegistry creates an empty registry for structured agent tools.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]Tool{}, aliases: map[string]string{}}
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

// RegisterAlias maps a legacy tool name to a canonical tool implementation.
func (r *ToolRegistry) RegisterAlias(alias, canonical string) error {
	if r == nil {
		return fmt.Errorf("tool registry is nil")
	}
	alias = strings.TrimSpace(alias)
	canonical = strings.TrimSpace(canonical)
	if alias == "" || canonical == "" {
		return fmt.Errorf("tool alias and canonical name are required")
	}
	if _, ok := r.tools[canonical]; !ok {
		return fmt.Errorf("canonical tool %s is not registered", canonical)
	}
	if _, exists := r.tools[alias]; exists {
		return fmt.Errorf("tool alias %s conflicts with registered tool", alias)
	}
	r.aliases[alias] = canonical
	return nil
}

// Get returns a registered tool by name.
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	if canonical, ok := r.aliases[name]; ok {
		name = canonical
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
	if result.ObservationData == nil {
		result.ObservationData = result.Data
	}
	return result
}

type RegistryOptions struct {
	Snapshot    SnapshotFunc
	Retriever   Retriever
	Policy      *RemediationPolicy
	Prometheus  PrometheusRangeClient
	Loki        LokiQueryClient
	ToolTimeout time.Duration
	MCPProvider *MCPProvider
}

type PrometheusRangeClient interface {
	Configured() bool
	QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*prometheus.QueryRangeResult, error)
}

type LokiQueryClient interface {
	Configured() bool
	QueryPodLogs(ctx context.Context, namespace, podName, containerName string, start, end time.Time) ([]loki.LogEntry, error)
}

// NewDefaultRegistry wires the MVP read-only tools used by the agent runtime.
func NewDefaultRegistry(opts RegistryOptions) (*ToolRegistry, error) {
	registry := NewToolRegistry()
	tools := []Tool{
		newSnapshotTool("k8s.get_pod", "Load pod snapshot and cache diagnostic context.", true, opts.Snapshot, podObservation),
		newSnapshotTool("k8s.get_events", "Read pod Events from cached Kubernetes snapshot.", false, opts.Snapshot, eventsObservation),
		newSnapshotTool("k8s.get_logs", "Read previous/current pod logs from cached Kubernetes snapshot.", false, opts.Snapshot, logsObservation),
		newSnapshotTool("k8s.get_topology", "Read workload, service, endpoint, and node topology.", false, opts.Snapshot, topologyObservation),
		newSnapshotTool("k8s.get_pvc", "Read PVC status referenced by the pod.", false, opts.Snapshot, pvcObservation),
		newRunbookSearchTool(opts.Retriever),
		newPrometheusQueryTool(opts.Prometheus),
		newLokiQueryTool(opts.Loki),
		newRemediationGenerateTool(opts.Policy),
		newRemediationDryRunTool(opts.Policy),
	}
	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	for alias, canonical := range map[string]string{
		"k8s.get_previous_logs":     "k8s.get_logs",
		"k8s.get_pvc_status":        "k8s.get_pvc",
		"remediation.dry_run_patch": "remediation.dry_run",
	} {
		if err := registry.RegisterAlias(alias, canonical); err != nil {
			return nil, err
		}
	}

	// Register MCP tools from external MCP servers (e.g., Grafana, PostgreSQL).
	if opts.MCPProvider != nil {
		for _, tool := range opts.MCPProvider.Tools() {
			if err := registry.Register(tool); err != nil {
				// Skip MCP tools that conflict with built-in names.
				continue
			}
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
					return ToolResult{ToolName: name, Success: false, Error: err.Error(), Observation: "快照采集失败"}
				}
				state.DiagnosticContext = diagCtx
			}
			return observe(state.DiagnosticContext)
		},
	}
}

func podObservation(ctx *diagnostic.DiagnosticContext) ToolResult {
	if ctx == nil || ctx.Pod == nil {
		return failedTool("k8s.get_pod", "Pod 快照为空")
	}
	return ToolResult{
		Success:     true,
		Observation: fmt.Sprintf("Pod %s/%s phase=%s containers=%d", ctx.Namespace, ctx.PodName, ctx.Pod.Status.Phase, len(ctx.Pod.Spec.Containers)),
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
		return failedTool("k8s.get_events", "诊断上下文为空")
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
	return ToolResult{Success: true, Observation: fmt.Sprintf("已采集 %d 条 Pod 事件", len(ctx.Events)), EvidenceRecords: records, Data: ctx.Events}
}

func logsObservation(ctx *diagnostic.DiagnosticContext) ToolResult {
	if ctx == nil {
		return failedTool("k8s.get_logs", "诊断上下文为空")
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
	return ToolResult{Success: true, Observation: fmt.Sprintf("已采集 %d 个容器的日志", len(ctx.Logs)), EvidenceRecords: records, Data: ctx.Logs}
}

func topologyObservation(ctx *diagnostic.DiagnosticContext) ToolResult {
	if ctx == nil || ctx.Topology == nil {
		return ToolResult{Success: true, Observation: "拓扑信息不可用", Data: nil}
	}
	return ToolResult{Success: true, Observation: "拓扑快照已采集", Data: ctx.Topology}
}

func pvcObservation(ctx *diagnostic.DiagnosticContext) ToolResult {
	if ctx == nil {
		return failedTool("k8s.get_pvc", "诊断上下文为空")
	}
	records := make([]diagnostic.EvidenceRecord, 0, len(ctx.PVCs))
	for _, pvc := range ctx.PVCs {
		severity := "info"
		if pvc.Phase != "Bound" {
			severity = "warning"
		}
		records = append(records, diagnostic.EvidenceRecord{
			SourceType: "k8s_pvc",
			Title:      "PVC/PV/StorageClass topology",
			Content:    fmt.Sprintf("pvc=%s phase=%s storageClass=%s volumeName=%s pvPhase=%s reclaimPolicy=%s provisioner=%s bindingMode=%s selectedNode=%s capacity=%s", pvc.Name, pvc.Phase, pvc.StorageClass, pvc.VolumeName, pvc.PVPhase, pvc.ReclaimPolicy, pvc.StorageClassProvisioner, pvc.VolumeBindingMode, pvc.SelectedNode, pvc.Capacity),
			Severity:   severity,
			Raw:        pvc,
			Timestamp:  time.Now(),
		})
	}
	return ToolResult{Success: true, Observation: fmt.Sprintf("已采集 %d 个 PVC 状态", len(ctx.PVCs)), EvidenceRecords: records, Data: ctx.PVCs}
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
				return ToolResult{Success: true, Observation: "诊断手册检索器未配置"}
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
			return ToolResult{Success: true, Observation: fmt.Sprintf("诊断手册命中 %d 条", len(hits)), EvidenceRecords: records, Data: hits}
		},
	}
}

func newPrometheusQueryTool(client PrometheusRangeClient) Tool {
	return simpleTool{
		meta: ToolMetadata{
			Name:        "prometheus.query_range",
			Description: "Query Prometheus range API and return structured metric evidence.",
			InputSchema: map[string]string{"query": "string", "start": "RFC3339", "end": "RFC3339", "step_seconds": "int"},
			RiskLevel:   "low",
			ReadOnly:    true,
			Timeout:     10 * time.Second,
		},
		fn: func(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
			query := stringInput(input, "query")
			if query == "" {
				return ToolResult{Success: false, Observation: "Prometheus 查询为空", Error: "prometheus query is empty", MissingEvidence: []string{"metrics query"}}
			}
			if client == nil || !client.Configured() {
				return ToolResult{Success: false, Observation: "Prometheus 未配置", Error: "prometheus is not configured", MissingEvidence: []string{"prometheus metrics"}}
			}
			start, end, err := rangeWindowInput(input, state)
			if err != nil {
				return failedTool("prometheus.query_range", err.Error())
			}
			step := durationSecondsInput(input, "step_seconds", 30*time.Second)
			result, err := client.QueryRange(ctx, query, start, end, step)
			if err != nil {
				return ToolResult{Success: false, Observation: "Prometheus 查询失败", Error: err.Error(), MissingEvidence: []string{"prometheus metrics"}, Data: map[string]interface{}{"query": query, "start": start, "end": end, "step": step.String()}}
			}
			samples := countPrometheusSamples(result)
			warnings := append([]string{}, result.Warnings...)
			missing := []string{}
			if samples == 0 {
				warnings = append(warnings, "prometheus returned no samples")
				missing = append(missing, "prometheus samples")
			}
			return ToolResult{
				Success:         true,
				Observation:     fmt.Sprintf("Prometheus 区间查询返回 samples=%d query=%s", samples, query),
				ObservationData: result,
				Warnings:        warnings,
				MissingEvidence: missing,
				EvidenceRecords: []diagnostic.EvidenceRecord{{
					SourceType: "prometheus",
					Title:      "Prometheus query_range",
					Content:    fmt.Sprintf("query=%s start=%s end=%s step=%s series=%d samples=%d", query, start.Format(time.RFC3339), end.Format(time.RFC3339), step.String(), len(result.Series), samples),
					Severity:   severityForSampleCount(samples),
					Raw:        result,
					Timestamp:  time.Now(),
				}},
				Data: result,
			}
		},
	}
}

func newLokiQueryTool(client LokiQueryClient) Tool {
	return simpleTool{
		meta: ToolMetadata{
			Name:        "loki.query_logs",
			Description: "Query Loki for pod log entries in a fault window.",
			InputSchema: map[string]string{"namespace": "string", "pod_name": "string", "container_name": "string", "start": "RFC3339", "end": "RFC3339"},
			RiskLevel:   "low",
			ReadOnly:    true,
			Timeout:     10 * time.Second,
		},
		fn: func(ctx context.Context, input map[string]interface{}, state *ToolState) ToolResult {
			namespace := firstNonEmpty(stringInput(input, "namespace"), goalNamespace(state))
			podName := firstNonEmpty(stringInput(input, "pod_name"), goalPodName(state))
			containerName := firstNonEmpty(stringInput(input, "container_name"), goalContainerName(state))
			if namespace == "" || podName == "" {
				return ToolResult{Success: false, Observation: "Loki 查询需要 namespace 和 pod_name", Error: "loki query requires namespace and pod_name", MissingEvidence: []string{"loki logs"}}
			}
			if client == nil || !client.Configured() {
				return ToolResult{Success: false, Observation: "Loki 未配置", Error: "loki is not configured", MissingEvidence: []string{"loki logs"}}
			}
			start, end, err := rangeWindowInput(input, state)
			if err != nil {
				return failedTool("loki.query_logs", err.Error())
			}
			entries, err := client.QueryPodLogs(ctx, namespace, podName, containerName, start, end)
			if err != nil {
				return ToolResult{Success: false, Observation: "Loki 查询失败", Error: err.Error(), MissingEvidence: []string{"loki logs"}, Data: map[string]interface{}{"namespace": namespace, "pod_name": podName, "container_name": containerName, "start": start, "end": end}}
			}
			warnings := []string{}
			missing := []string{}
			if len(entries) == 0 {
				warnings = append(warnings, "loki returned no log entries")
				missing = append(missing, "loki log entries")
			}
			return ToolResult{
				Success:         true,
				Observation:     fmt.Sprintf("Loki 返回日志 entries=%d namespace=%s pod=%s", len(entries), namespace, podName),
				ObservationData: entries,
				Warnings:        warnings,
				MissingEvidence: missing,
				EvidenceRecords: []diagnostic.EvidenceRecord{{
					SourceType: "loki",
					Title:      "Loki pod logs",
					Content:    fmt.Sprintf("namespace=%s pod=%s container=%s window=%s..%s entries=%d", namespace, podName, containerName, start.Format(time.RFC3339), end.Format(time.RFC3339), len(entries)),
					Severity:   severityForSampleCount(len(entries)),
					Raw:        entries,
					Timestamp:  time.Now(),
				}},
				Data: entries,
			}
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
				return failedTool("remediation.generate_actions", "诊断报告尚未就绪")
			}
			actions := diagnostic.GenerateRemediationActions(state.DiagnosticContext, state.Report)
			if policy == nil {
				policy = NewRemediationPolicy(true)
			}
			actions, executions := policy.SanitizeActions(state.TaskID, actions)
			state.RemediationActions = actions
			state.Executions = executions
			state.Report.RemediationActions = actions
			return ToolResult{Success: true, Observation: fmt.Sprintf("生成 %d 条修复建议", len(actions)), Data: actions}
		},
	}
}

func newRemediationDryRunTool(policy *RemediationPolicy) Tool {
	return simpleTool{
		meta: ToolMetadata{
			Name:        "remediation.dry_run",
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

func rangeWindowInput(input map[string]interface{}, state *ToolState) (time.Time, time.Time, error) {
	end := time.Now()
	start := end.Add(-10 * time.Minute)
	if state != nil && state.DiagnosticContext != nil {
		if !state.DiagnosticContext.LogWindowStart.IsZero() && !state.DiagnosticContext.LogWindowEnd.IsZero() {
			start = state.DiagnosticContext.LogWindowStart
			end = state.DiagnosticContext.LogWindowEnd
		} else if !state.DiagnosticContext.FaultTime.IsZero() {
			start = state.DiagnosticContext.FaultTime.Add(-5 * time.Minute)
			end = state.DiagnosticContext.FaultTime.Add(5 * time.Minute)
		}
	} else if state != nil && state.Goal.AlertTime != nil && !state.Goal.AlertTime.IsZero() {
		start = state.Goal.AlertTime.Add(-5 * time.Minute)
		end = state.Goal.AlertTime.Add(5 * time.Minute)
	}
	if raw := stringInput(input, "start"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start: %w", err)
		}
		start = parsed
	}
	if raw := stringInput(input, "end"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end: %w", err)
		}
		end = parsed
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("end time is before start time")
	}
	return start, end, nil
}

func durationSecondsInput(input map[string]interface{}, key string, fallback time.Duration) time.Duration {
	if input == nil {
		return fallback
	}
	switch value := input[key].(type) {
	case int:
		if value > 0 {
			return time.Duration(value) * time.Second
		}
	case int64:
		if value > 0 {
			return time.Duration(value) * time.Second
		}
	case float64:
		if value > 0 {
			return time.Duration(value) * time.Second
		}
	}
	return fallback
}

func countPrometheusSamples(result *prometheus.QueryRangeResult) int {
	if result == nil {
		return 0
	}
	count := 0
	for _, series := range result.Series {
		count += len(series.Points)
	}
	return count
}

func severityForSampleCount(count int) string {
	if count == 0 {
		return "warning"
	}
	return "info"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func goalNamespace(state *ToolState) string {
	if state == nil {
		return ""
	}
	return state.Goal.Namespace
}

func goalPodName(state *ToolState) string {
	if state == nil {
		return ""
	}
	return state.Goal.PodName
}

func goalContainerName(state *ToolState) string {
	if state == nil {
		return ""
	}
	return state.Goal.ContainerName
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
