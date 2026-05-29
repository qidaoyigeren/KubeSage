package analyzer

import (
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

type PendingAnalyzer struct{}

// NewPendingAnalyzer creates the analyzer for pods stuck in Pending.
func NewPendingAnalyzer() *PendingAnalyzer {
	return &PendingAnalyzer{}
}

// Name returns the analyzer identifier used in reports.
func (a *PendingAnalyzer) Name() string { return "pod_pending" }

// Match decides whether pod status/events indicate scheduling failure.
func (a *PendingAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	if ctx.Pod == nil {
		return false
	}
	if ctx.Pod.Status.Phase == corev1.PodPending {
		return true
	}
	for _, condition := range ctx.Pod.Status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
			return true
		}
	}
	for _, event := range ctx.Events {
		if event.Reason == "FailedScheduling" {
			return true
		}
	}
	return false
}

// Analyze collects scheduling constraints, events, and node capacity evidence.
func (a *PendingAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	evidences := []diagnostic.EvidenceRecord{}
	actions := []string{"根据 FailedScheduling message 调整 requests、nodeSelector、affinity、tolerations 或 PVC。"}
	summary := "Pod 处于 Pending，调度器暂未找到满足约束的节点。"

	for _, event := range ctx.Events {
		if event.Reason != "FailedScheduling" {
			continue
		}
		evidences = append(evidences, eventEvidence(event, "critical"))
		message := strings.ToLower(event.Message)
		switch {
		case strings.Contains(message, "insufficient cpu"):
			summary = "调度失败原因包含 Insufficient cpu，集群可用 CPU 不满足 Pod requests。"
			actions = append(actions, "降低 CPU requests 或扩容节点。")
		case strings.Contains(message, "insufficient memory"):
			summary = "调度失败原因包含 Insufficient memory，集群可用内存不满足 Pod requests。"
			actions = append(actions, "降低 memory requests 或扩容节点。")
		case strings.Contains(message, "untolerated taint"):
			summary = "Pod 无法容忍目标节点污点。"
			actions = append(actions, "增加合适 tolerations，或选择无对应 taint 的节点池。")
		case strings.Contains(message, "node selector"):
			summary = "Pod nodeSelector 或 affinity 与当前节点标签不匹配。"
			actions = append(actions, "核对 nodeSelector、node affinity 和节点 label。")
		case strings.Contains(message, "unbound immediate persistentvolumeclaims"):
			summary = "Pod 依赖的 PVC 未绑定，导致无法调度。"
			actions = append(actions, "检查 PVC/PV/StorageClass 状态和容量。")
		}
	}

	for _, c := range ctx.Pod.Spec.Containers {
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "Pod resource requests",
			Content:    fmt.Sprintf("container=%s cpuRequest=%s memoryRequest=%s", c.Name, c.Resources.Requests.Cpu().String(), c.Resources.Requests.Memory().String()),
			Severity:   "info",
			Raw:        c.Resources.Requests,
			Timestamp:  time.Now(),
		})
	}
	evidences = append(evidences, diagnostic.EvidenceRecord{
		SourceType: "k8s_pod_status",
		Title:      "Scheduling constraints",
		Content:    fmt.Sprintf("nodeSelector=%v affinity=%v tolerations=%v", ctx.Pod.Spec.NodeSelector, ctx.Pod.Spec.Affinity, ctx.Pod.Spec.Tolerations),
		Severity:   "info",
		Raw:        ctx.Pod.Spec,
		Timestamp:  time.Now(),
	})
	for _, node := range ctx.NodeSnapshots {
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_node",
			Title:      "Node allocatable resource snapshot",
			Content:    fmt.Sprintf("node=%s ready=%v allocatableCPU=%s allocatableMemory=%s", node.Name, node.Ready, node.AllocatableCPU, node.AllocatableMemory),
			Severity:   "info",
			Raw:        node,
			Timestamp:  time.Now(),
		})
	}

	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "PodPending",
		RootCauseSummary: summary,
		ConfidenceScore:  0.86,
		Evidences:        evidences,
		ImpactAnalysis:   "Pod 尚未运行，业务副本数可能低于期望值。",
		SuggestedActions: actions,
		RiskLevel:        "medium",
		NeedHumanConfirm: true,
	}, nil
}
