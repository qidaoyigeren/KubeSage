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
	summary := "Pod was evicted by kubelet, usually because node resources or ephemeral storage crossed eviction thresholds."
	actions := []string{
		"Inspect node memory, disk, PID, and ephemeral-storage pressure around the eviction time.",
		"Check pod requests/limits and ephemeral-storage usage before rescheduling the workload.",
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
			summary = "Pod was evicted while the node reported DiskPressure; ephemeral storage or node disk exhaustion is the leading cause."
		}
		if node.MemoryPressure {
			summary = "Pod was evicted while the node reported MemoryPressure; node-level memory pressure is the leading cause."
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
	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "Evicted",
		RootCauseSummary: summary,
		ConfidenceScore:  evictedConfidence(evidences),
		Evidences:        evidences,
		ImpactAnalysis:   "Evicted pods are terminal and need a replacement pod; repeated evictions usually indicate node or request sizing issues.",
		SuggestedActions: actions,
		RiskLevel:        "medium",
		NeedHumanConfirm: true,
	}, nil
}

func evictedConfidence(evidences []diagnostic.EvidenceRecord) float64 {
	score := 0.74
	if hasEvidence(evidences, "k8s_event", "") {
		score += 0.08
	}
	if hasEvidence(evidences, "k8s_topology", "Eviction node") {
		score += 0.06
	}
	return clampConfidence(score)
}
