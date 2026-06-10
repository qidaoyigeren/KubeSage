package analyzer

import (
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
)

type EvictedAnalyzer struct{}

func NewEvictedAnalyzer() *EvictedAnalyzer {
	return &EvictedAnalyzer{}
}

func (a *EvictedAnalyzer) Name() string { return "evicted" }

func (a *EvictedAnalyzer) Metadata() diagnostic.AnalyzerMetadata {
	return diagnostic.AnalyzerMetadata{
		Name:             a.Name(),
		FaultType:        "Evicted",
		Priority:         75,
		MatchSignals:     []string{"pod.status.reason=Evicted", "event.message contains evicted", "node pressure"},
		RequiredEvidence: []string{"k8s_pod_status", "k8s_event", "k8s_topology", "k8s_node"},
	}
}

func (a *EvictedAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	if ctx == nil || ctx.Pod == nil {
		return false
	}
	if ctx.Pod.Status.Reason == "Evicted" || ctx.Pod.Status.Phase == corev1.PodFailed && strings.Contains(strings.ToLower(ctx.Pod.Status.Message), "evict") {
		return true
	}
	for _, event := range ctx.Events {
		if strings.Contains(strings.ToLower(event.Reason+" "+event.Message), "evict") {
			return true
		}
	}
	return false
}

func (a *EvictedAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	evidences := []diagnostic.EvidenceRecord{{
		SourceType: "k8s_pod_status",
		Title:      "Pod eviction status",
		Content:    fmt.Sprintf("phase=%s reason=%s message=%s node=%s", ctx.Pod.Status.Phase, ctx.Pod.Status.Reason, ctx.Pod.Status.Message, ctx.Pod.Spec.NodeName),
		Severity:   "critical",
		Raw:        ctx.Pod.Status,
		Timestamp:  time.Now(),
	}}
	summary := "Evicted: Pod 被 kubelet 驱逐，通常是节点资源或临时存储 ephemeral-storage 超过驱逐阈值。"
	actions := []string{
		"检查驱逐时间附近的节点内存、磁盘、PID 和 ephemeral-storage 压力。",
		"重新调度工作负载前，确认 Pod requests/limits 和 ephemeral-storage 使用量是否合理。",
	}
	for _, event := range ctx.Events {
		if strings.Contains(strings.ToLower(event.Reason+" "+event.Message), "evict") {
			evidences = append(evidences, eventEvidence(event, "critical"))
		}
	}
	if ctx.Topology != nil && ctx.Topology.Node != nil {
		node := ctx.Topology.Node
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_topology",
			Title:      "Eviction node pressure context",
			Content:    fmt.Sprintf("node=%s ready=%t memoryPressure=%t diskPressure=%t pidPressure=%t", node.Name, node.Ready, node.MemoryPressure, node.DiskPressure, node.PIDPressure),
			Severity:   "warning",
			Raw:        node,
			Timestamp:  time.Now(),
		})
		if node.DiskPressure {
			summary = "Evicted node DiskPressure ephemeral-storage: Pod 被驱逐时节点存在 DiskPressure，临时存储或节点磁盘耗尽是优先怀疑根因。"
		}
		if node.MemoryPressure {
			summary = "Evicted node MemoryPressure memory: Pod 被驱逐时节点存在 MemoryPressure，节点级内存压力是优先怀疑根因。"
		}
		if node.PIDPressure {
			summary = "Evicted node PIDPressure pid: Pod 被驱逐时节点存在 PIDPressure，节点 PID 资源耗尽是优先怀疑根因。"
		}
	}
	for _, container := range ctx.Pod.Spec.Containers {
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "Container resource requests and limits",
			Content:    fmt.Sprintf("container=%s requests=%v limits=%v", container.Name, container.Resources.Requests, container.Resources.Limits),
			Severity:   "info",
			Raw:        container.Resources,
			Timestamp:  time.Now(),
		})
	}
	// Record QoS class — per K8s docs, eviction order is:
	// BestEffort (evicted first) → Burstable → Guaranteed (evicted last).
	if ctx.Pod.Status.QOSClass != "" {
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "Pod QoS class (affects eviction order)",
			Content:    fmt.Sprintf("qosClass=%s", string(ctx.Pod.Status.QOSClass)),
			Severity:   "info",
			Raw:        ctx.Pod.Status.QOSClass,
			Timestamp:  time.Now(),
		})
	}
	// Detect ephemeral-storage usage — per K8s docs, ephemeral-storage
	// exhaustion is a common cause of DiskPressure eviction.
	if ctx.Topology != nil && ctx.Topology.Node != nil && ctx.Topology.Node.DiskPressure {
		summary += " 同时检查 ephemeral-storage 使用量和 Pod 临时文件写入。"
		actions = append(actions, "检查容器 ephemeral-storage 使用量、日志大小和临时文件写入。")
	}
	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "Evicted",
		RootCauseSummary: summary,
		ConfidenceScore:  evictedConfidence(evidences),
		Evidences:        evidences,
		ImpactAnalysis:   "被驱逐的 Pod 已进入终态，需要由控制器创建替代 Pod；如果反复驱逐，通常说明节点资源或资源请求配置存在问题。",
		SuggestedActions: actions,
		RiskLevel:        "medium",
		NeedHumanConfirm: true,
	}, nil
}

// evictedConfidence adjusts confidence based on eviction evidence specificity.
// Per K8s docs, node-pressure eviction is triggered by kubelet when:
//   - MemoryPressure: available memory below eviction threshold
//   - DiskPressure: available disk space or inodes below thresholds
//   - PIDPressure: process count exceeds limit
//   - QoS class affects eviction order: BestEffort first, Guaranteed last
//   - ephemeral-storage exhaustion is a common disk pressure cause
func evictedConfidence(evidences []diagnostic.EvidenceRecord) float64 {
	score := 0.74
	if hasEvidence(evidences, "k8s_event", "") {
		score += 0.08
	}
	if hasEvidence(evidences, "k8s_topology", "Eviction node") {
		score += 0.06
	}
	// Specific eviction cause detection — more specific evidence = higher confidence.
	if hasEvidenceContent(evidences, "DiskPressure") || hasEvidenceContent(evidences, "diskpressure") {
		// Disk/ephemeral-storage pressure is the most common eviction cause.
		score += 0.04
	}
	if hasEvidenceContent(evidences, "MemoryPressure") || hasEvidenceContent(evidences, "memorypressure") {
		score += 0.03
	}
	if hasEvidenceContent(evidences, "PIDPressure") || hasEvidenceContent(evidences, "pidpressure") {
		score += 0.03
	}
	// QoS class affects eviction priority — per K8s docs:
	// BestEffort pods are evicted first, Guaranteed last.
	if hasQoSEvidence(evidences, "BestEffort") {
		// BestEffort pod eviction is expected under any pressure — less diagnostic.
		score += 0.01
	}
	if hasQoSEvidence(evidences, "Guaranteed") {
		// Guaranteed pod eviction means severe pressure — stronger signal.
		score += 0.03
	}
	// ephemeral-storage specific eviction detection.
	if hasEvidenceContent(evidences, "ephemeral") || hasEvidenceContent(evidences, "temporary") {
		score += 0.03
	}
	return clampConfidence(score)
}
