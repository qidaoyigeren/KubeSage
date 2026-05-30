package analyzer

import (
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
)

// NodeNotReadyAnalyzer detects pods affected by node NotReady conditions.
type NodeNotReadyAnalyzer struct{}

// NewNodeNotReadyAnalyzer creates the analyzer for NodeNotReady cases.
func NewNodeNotReadyAnalyzer() *NodeNotReadyAnalyzer {
	return &NodeNotReadyAnalyzer{}
}

// Name returns the analyzer identifier used in reports.
func (a *NodeNotReadyAnalyzer) Name() string { return "nodenotready" }

func (a *NodeNotReadyAnalyzer) Metadata() diagnostic.AnalyzerMetadata {
	return diagnostic.AnalyzerMetadata{
		Name:             a.Name(),
		FaultType:        "NodeNotReady",
		Priority:         55,
		MatchSignals:     []string{"node.Ready=False", "node.MemoryPressure=True", "node.DiskPressure=True"},
		RequiredEvidence: []string{"k8s_topology", "k8s_event"},
	}
}

// Match decides whether the pod's node is in a NotReady state.
func (a *NodeNotReadyAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	if ctx.Topology == nil || ctx.Topology.Node == nil {
		return false
	}
	return !ctx.Topology.Node.Ready
}

// Analyze collects node condition evidence for NodeNotReady.
func (a *NodeNotReadyAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	now := time.Now()
	evidence := []diagnostic.EvidenceRecord{}
	node := ctx.Topology.Node

	// Node Ready=False evidence.
	if !node.Ready {
		evidence = append(evidence, diagnostic.EvidenceRecord{
			SourceType: "k8s_topology",
			Title:      "Node NotReady",
			Content:    fmt.Sprintf("Node %s is in NotReady state", node.Name),
			Severity:   "critical",
			Timestamp:  now,
		})
	}

	// Node pressure conditions.
	if node.MemoryPressure {
		evidence = append(evidence, diagnostic.EvidenceRecord{
			SourceType: "k8s_topology",
			Title:      "Node MemoryPressure",
			Content:    fmt.Sprintf("Node %s has MemoryPressure condition", node.Name),
			Severity:   "warning",
			Timestamp:  now,
		})
	}
	if node.DiskPressure {
		evidence = append(evidence, diagnostic.EvidenceRecord{
			SourceType: "k8s_topology",
			Title:      "Node DiskPressure",
			Content:    fmt.Sprintf("Node %s has DiskPressure condition", node.Name),
			Severity:   "warning",
			Timestamp:  now,
		})
	}
	if node.PIDPressure {
		evidence = append(evidence, diagnostic.EvidenceRecord{
			SourceType: "k8s_topology",
			Title:      "Node PIDPressure",
			Content:    fmt.Sprintf("Node %s has PIDPressure condition", node.Name),
			Severity:   "warning",
			Timestamp:  now,
		})
	}

	// Node-related events.
	for _, event := range ctx.Events {
		if strings.Contains(event.Reason, "Node") || strings.Contains(event.Reason, "NotReady") ||
			strings.Contains(event.Message, "node") || strings.Contains(event.Message, "NotReady") {
			severity := "warning"
			if event.Type == "Warning" {
				severity = "warning"
			} else {
				severity = "info"
			}
			evidence = append(evidence, diagnostic.EvidenceRecord{
				SourceType: "k8s_event",
				Title:      event.Reason,
				Content:    event.Message,
				Severity:   severity,
				Timestamp:  event.LastTimestamp.Time,
			})
		}
	}

	// Confidence starts high because node NotReady is a strong signal.
	confidence := 0.85
	if node.MemoryPressure || node.DiskPressure {
		confidence += 0.05
	}

	summary := fmt.Sprintf("Pod is running on node %s which is in NotReady state", node.Name)
	var pressureConditions []string
	if node.MemoryPressure {
		pressureConditions = append(pressureConditions, "MemoryPressure")
	}
	if node.DiskPressure {
		pressureConditions = append(pressureConditions, "DiskPressure")
	}
	if node.PIDPressure {
		pressureConditions = append(pressureConditions, "PIDPressure")
	}
	if len(pressureConditions) > 0 {
		summary += " with " + strings.Join(pressureConditions, ", ") + " conditions"
	}

	return &diagnostic.AnalyzeResult{
		FaultType:        "NodeNotReady",
		ConfidenceScore:  clampConfidence(confidence),
		RootCauseSummary: summary,
		Evidences:        evidence,
	}, nil
}
