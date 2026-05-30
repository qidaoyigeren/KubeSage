package diagnostic

import (
	"strconv"
	"strings"
	"time"

	"kubesage/internal/observability"
)

type DiagnosisEngine struct {
	analyzers []Analyzer
}

// NewDiagnosisEngine builds a rule engine with the given analyzers in order.
func NewDiagnosisEngine(analyzers ...Analyzer) *DiagnosisEngine {
	return &DiagnosisEngine{analyzers: analyzers}
}

// Diagnose runs all matching analyzers and returns an aggregated report.
func (e *DiagnosisEngine) Diagnose(ctx *DiagnosticContext) (*Report, error) {
	var results []*AnalyzeResult
	for _, analyzer := range e.analyzers {
		if analyzer.Match(ctx) {
			observability.IncAnalyzerMatchTotal(analyzer.Name())
			result, err := analyzer.Analyze(ctx)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
	}

	if len(results) == 0 {
		report := &Report{
			Namespace:        ctx.Namespace,
			PodName:          ctx.PodName,
			FaultType:        "unknown",
			RootCauseSummary: "未命中内置 MVP 规则，请结合 Pod 状态、Events、日志继续人工确认。",
			ConfidenceScore:  0.3,
			Evidences: []EvidenceRecord{{
				SourceType: "diagnostic_engine",
				Title:      "No analyzer matched",
				Content:    "CrashLoopBackOff、OOMKilled、Pod Pending、Probe Failed 规则均未命中。",
				Severity:   "info",
				Timestamp:  time.Now(),
			}},
			ImpactAnalysis:   "影响范围需要结合 Service endpoints、Deployment 副本状态和业务告警继续确认。",
			SuggestedActions: []string{"查看 Pod events、容器日志和最近发布变更。", "必要时补充 Prometheus 与 Loki 数据源后重新诊断。"},
			RiskLevel:        "medium",
			NeedHumanConfirm: true,
		}
		enrichReportWithTopology(ctx, report)
		AttachRemediationActions(ctx, report)
		return report, nil
	}

	report := aggregate(ctx, results)
	enrichReportWithTopology(ctx, report)
	AttachRemediationActions(ctx, report)
	return report, nil
}

// aggregate merges multiple analyzer results and keeps the highest-confidence
// result as the main diagnosis summary.
func aggregate(ctx *DiagnosticContext, results []*AnalyzeResult) *Report {
	best := results[0]
	for _, result := range results[1:] {
		if result.ConfidenceScore > best.ConfidenceScore {
			best = result
		}
	}

	evidences := make([]EvidenceRecord, 0)
	actions := make([]string, 0)
	faultTypes := make([]string, 0)
	for _, result := range results {
		evidences = append(evidences, result.Evidences...)
		actions = append(actions, result.SuggestedActions...)
		faultTypes = append(faultTypes, result.FaultType)
	}

	faultType := best.FaultType
	if len(results) > 1 {
		faultType = strings.Join(uniqueStrings(faultTypes), ",")
	}

	return &Report{
		Namespace:        ctx.Namespace,
		PodName:          ctx.PodName,
		FaultType:        faultType,
		RootCauseSummary: best.RootCauseSummary,
		ConfidenceScore:  best.ConfidenceScore,
		Evidences:        evidences,
		ImpactAnalysis:   best.ImpactAnalysis,
		SuggestedActions: uniqueStrings(actions),
		RiskLevel:        best.RiskLevel,
		NeedHumanConfirm: true,
	}
}

// enrichReportWithTopology adds topology evidence and appends topology-derived
// impact analysis to the final report.
func enrichReportWithTopology(ctx *DiagnosticContext, report *Report) {
	if ctx == nil || report == nil || ctx.Topology == nil {
		return
	}
	report.Evidences = append(report.Evidences, topologyEvidence(ctx.Topology))
	impact := topologyImpactAnalysis(ctx.Topology)
	if impact == "" {
		return
	}
	if strings.TrimSpace(report.ImpactAnalysis) == "" {
		report.ImpactAnalysis = impact
		return
	}
	report.ImpactAnalysis = report.ImpactAnalysis + "\n" + impact
}

// topologyEvidence converts the collected Kubernetes topology into report
// evidence.
func topologyEvidence(topology *TopologyInfo) EvidenceRecord {
	return EvidenceRecord{
		SourceType: "k8s_topology",
		Title:      "Kubernetes workload, service, and node topology",
		Content:    topologyEvidenceContent(topology),
		Severity:   topologySeverity(topology),
		Raw:        topology,
		Timestamp:  time.Now(),
	}
}

// topologyEvidenceContent builds a compact text summary of topology status.
func topologyEvidenceContent(topology *TopologyInfo) string {
	parts := []string{}
	if topology.ReplicaSetName != "" {
		parts = append(parts, "replicaSet="+topology.ReplicaSetName)
	}
	if topology.DeploymentName != "" {
		parts = append(parts, "deployment="+topology.DeploymentName)
	}
	if topology.Workload != nil {
		parts = append(parts,
			"desiredReplicas="+itoa32(topology.Workload.DesiredReplicas),
			"otherPods="+itoa(topology.Workload.OtherPods),
			"otherRunning="+itoa(topology.Workload.OtherRunning),
			"otherReady="+itoa(topology.Workload.OtherReady),
			"otherAbnormal="+itoa(topology.Workload.OtherAbnormal),
			"otherRestartCount="+itoa32(topology.Workload.OtherRestartCount),
		)
	}
	if len(topology.SelectedServices) > 0 {
		serviceParts := make([]string, 0, len(topology.SelectedServices))
		for _, service := range topology.SelectedServices {
			serviceParts = append(serviceParts, service.Name+" endpoints="+itoa(service.AvailableEndpoints))
		}
		parts = append(parts, "services=["+strings.Join(serviceParts, ", ")+"]")
	}
	if topology.Node != nil {
		parts = append(parts,
			"node="+topology.Node.Name,
			"nodeReady="+boolString(topology.Node.Ready),
			"memoryPressure="+boolString(topology.Node.MemoryPressure),
			"diskPressure="+boolString(topology.Node.DiskPressure),
			"pidPressure="+boolString(topology.Node.PIDPressure),
		)
	}
	return strings.Join(parts, " ")
}

// topologySeverity classifies topology evidence severity from service and node
// health.
func topologySeverity(topology *TopologyInfo) string {
	if serviceEndpointsUnavailable(topology) || nodeHasPressure(topology) {
		return "warning"
	}
	return "info"
}

// topologyImpactAnalysis derives user-facing impact text from topology facts.
func topologyImpactAnalysis(topology *TopologyInfo) string {
	impacts := []string{}
	if topology.Workload != nil {
		if topology.Workload.DesiredReplicas > 1 && topology.Workload.OtherReady > 0 && topology.Workload.OtherAbnormal == 0 {
			impacts = append(impacts, "Deployment 为多副本且其他副本 Ready，当前单 Pod 故障对整体服务影响较低。")
		}
		if topology.Workload.DesiredReplicas <= 1 || topology.Workload.OtherReady == 0 {
			impacts = append(impacts, "Deployment 缺少可用的其他副本，当前 Pod 故障可能直接影响服务可用性。")
		}
		if topology.Workload.OtherAbnormal > 0 {
			impacts = append(impacts, "同 Deployment 下存在异常副本，故障可能不是单 Pod 孤例。")
		}
	}
	if len(topology.SelectedServices) > 0 {
		if serviceEndpointsUnavailable(topology) {
			impacts = append(impacts, "命中当前 Pod 的 Service endpoints 全部不可用，业务入口影响较高。")
		} else {
			impacts = append(impacts, "命中当前 Pod 的 Service 仍存在可用 endpoints。")
		}
	}
	if nodeHasPressure(topology) {
		impacts = append(impacts, "当前 Pod 所在 Node 存在 Pressure condition，需关注节点级资源问题。")
	}
	return strings.Join(impacts, "\n")
}

// serviceEndpointsUnavailable reports whether every selected service has zero
// available endpoints.
func serviceEndpointsUnavailable(topology *TopologyInfo) bool {
	if topology == nil || len(topology.SelectedServices) == 0 {
		return false
	}
	for _, service := range topology.SelectedServices {
		if service.AvailableEndpoints > 0 {
			return false
		}
	}
	return true
}

// nodeHasPressure reports whether the pod's node has any pressure condition.
func nodeHasPressure(topology *TopologyInfo) bool {
	if topology == nil || topology.Node == nil {
		return false
	}
	return topology.Node.MemoryPressure || topology.Node.DiskPressure || topology.Node.PIDPressure
}

// itoa formats an int without pulling formatting logic into every caller.
func itoa(v int) string {
	return strconv.Itoa(v)
}

// itoa32 formats an int32 value.
func itoa32(v int32) string {
	return strconv.FormatInt(int64(v), 10)
}

// boolString formats a boolean value.
func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// uniqueStrings removes duplicate non-empty strings while preserving order.
func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}
