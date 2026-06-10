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

func (a *PendingAnalyzer) Metadata() diagnostic.AnalyzerMetadata {
	return diagnostic.AnalyzerMetadata{
		Name:             a.Name(),
		FaultType:        "PodPending",
		Priority:         70,
		MatchSignals:     []string{"pod.phase=Pending", "PodScheduled=False", "event.reason=FailedScheduling"},
		RequiredEvidence: []string{"k8s_event", "k8s_pvc", "k8s_node", "k8s_storage_topology", "correlation_evidence"},
	}
}

// Match decides whether pod status/events indicate scheduling failure.
func (a *PendingAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	if ctx.Pod == nil {
		return false
	}
	for _, status := range append(ctx.Pod.Status.InitContainerStatuses, ctx.Pod.Status.ContainerStatuses...) {
		if imagePullWaiting(status.State.Waiting) {
			return false
		}
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
	summary := "PodPending FailedScheduling: Pod 处于 Pending，调度器暂未找到满足约束的节点。"

	for _, event := range ctx.Events {
		if event.Reason != "FailedScheduling" {
			continue
		}
		evidences = append(evidences, eventEvidence(event, "critical"))
		message := strings.ToLower(event.Message)
		switch {
		case strings.Contains(message, "insufficient cpu"):
			summary = "PodPending FailedScheduling Insufficient cpu requests: 调度失败原因包含 Insufficient cpu，集群可用 CPU 不满足 Pod requests。"
			actions = append(actions, "降低 CPU requests 或扩容节点。")
		case strings.Contains(message, "insufficient memory"):
			summary = "PodPending FailedScheduling Insufficient memory requests: 调度失败原因包含 Insufficient memory，集群可用内存不满足 Pod requests。"
			actions = append(actions, "降低 memory requests 或扩容节点。")
		case strings.Contains(message, "insufficient ephemeral-storage"):
			// Per K8s docs: ephemeral-storage is a schedulable resource.
			summary = "PodPending FailedScheduling Insufficient ephemeral-storage: 调度失败原因包含 Insufficient ephemeral-storage，节点临时存储不满足 Pod requests。"
			actions = append(actions, "降低 ephemeral-storage requests 或清理节点临时存储。")
		case strings.Contains(message, "untolerated taint"):
			summary = "PodPending FailedScheduling untolerated taint tolerations: Pod 无法容忍目标节点污点。"
			actions = append(actions, "增加合适 tolerations，或选择无对应 taint 的节点池。")
		case strings.Contains(message, "node selector"):
			summary = "PodPending FailedScheduling node selector nodeSelector: Pod nodeSelector 或 affinity 与当前节点标签不匹配。"
			actions = append(actions, "核对 nodeSelector、node affinity 和节点 label。")
		case strings.Contains(message, "unbound immediate persistentvolumeclaims"):
			summary = "PodPending unbound PersistentVolumeClaim PVC not Bound: Pod 依赖的 PVC 未绑定，导致无法调度。"
			actions = append(actions, "检查 PVC/PV/StorageClass 状态和容量。")
		case strings.Contains(message, "exceeded quota") || strings.Contains(message, "resourcequota"):
			// Per K8s docs: ResourceQuota can prevent scheduling.
			summary = "PodPending FailedScheduling ResourceQuota exceeded: 命名空间的 ResourceQuota 已满，无法为新 Pod 分配资源。"
			actions = append(actions, "检查命名空间 ResourceQuota 使用情况，清理不需要的资源或提升配额。")
		case strings.Contains(message, "preempted") || strings.Contains(message, "preemption"):
			// Per K8s docs: higher-priority pods can preempt lower-priority ones.
			summary = "PodPending FailedScheduling preemption: Pod 可能因优先级不足被更高优先级 Pod 抢占。"
			actions = append(actions, "检查 PriorityClass 配置和集群中高优先级 Pod 的调度情况。")
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
	for _, pvc := range ctx.PVCs {
		severity := "info"
		if pvc.Phase != string(corev1.ClaimBound) {
			severity = "critical"
			summary = "PodPending unbound PersistentVolumeClaim PVC Pending: Pod 依赖的 PVC 未处于 Bound 状态，导致无法调度或启动。"
			actions = append(actions, "优先检查 PVC/PV/StorageClass 绑定状态，而不是只调整 Pod 调度约束。")
		}
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pvc",
			Title:      "PVC/PV/StorageClass topology",
			Content:    fmt.Sprintf("pvc=%s phase=%s storageClass=%s volumeName=%s pvPhase=%s reclaimPolicy=%s provisioner=%s bindingMode=%s selectedNode=%s capacity=%s", pvc.Name, pvc.Phase, pvc.StorageClass, pvc.VolumeName, pvc.PVPhase, pvc.ReclaimPolicy, pvc.StorageClassProvisioner, pvc.VolumeBindingMode, pvc.SelectedNode, pvc.Capacity),
			Severity:   severity,
			Raw:        pvc,
			Timestamp:  time.Now(),
		})
	}

	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "PodPending",
		RootCauseSummary: summary,
		ConfidenceScore:  pendingConfidence(evidences),
		Evidences:        evidences,
		ImpactAnalysis:   "Pod 尚未运行，业务副本数可能低于期望值。",
		SuggestedActions: actions,
		RiskLevel:        "medium",
		NeedHumanConfirm: true,
	}, nil
}

// pendingConfidence weighs scheduler events, PVC status, node snapshots, and
// specific scheduling failure reasons. Per K8s docs, Pending can be caused by:
//   - Insufficient resources (CPU, memory, ephemeral-storage)
//   - Node taints without matching tolerations
//   - nodeSelector/affinity mismatches
//   - Unbound PVCs
//   - ResourceQuota limits
//   - PriorityClass preemption
func pendingConfidence(evidences []diagnostic.EvidenceRecord) float64 {
	score := 0.70
	if hasEvidence(evidences, "k8s_event", "FailedScheduling") {
		score += 0.08
	}
	if hasEvidence(evidences, "k8s_pvc", "") {
		score += 0.05
	}
	if hasEvidence(evidences, "k8s_node", "") {
		score += 0.03
	}
	// Specific scheduling failure reason boosts — more specific = higher confidence.
	if hasEvidenceContent(evidences, "insufficient cpu") || hasEvidenceContent(evidences, "insufficient memory") {
		score += 0.04
	}
	if hasEvidenceContent(evidences, "insufficient ephemeral-storage") {
		score += 0.04
	}
	if hasEvidenceContent(evidences, "untolerated taint") {
		score += 0.03
	}
	if hasEvidenceContent(evidences, "unbound immediate persistentvolumeclaims") {
		score += 0.04
	}
	if hasEvidenceContent(evidences, "exceeded quota") || hasEvidenceContent(evidences, "resourcequota") {
		score += 0.03
	}
	return clampConfidence(score)
}
