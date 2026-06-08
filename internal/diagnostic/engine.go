package diagnostic

import (
	"fmt"
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
		report.PrimaryRootCause = &RootCauseFactor{
			FaultType:        report.FaultType,
			Summary:          report.RootCauseSummary,
			ConfidenceScore:  report.ConfidenceScore,
			ContributingRole: "primary",
		}
		enrichReportWithTopology(ctx, report)
		enrichReportWithCorrelation(ctx, report)
		enrichReportWithMetricTrends(ctx, report)
		AttachRemediationActions(ctx, report)
		return report, nil
	}

	report := aggregate(ctx, results)
	enrichReportWithTopology(ctx, report)
	enrichReportWithCorrelation(ctx, report)
	enrichReportWithMetricTrends(ctx, report)
	AttachRemediationActions(ctx, report)
	return report, nil
}

// aggregate merges multiple analyzer results and keeps the strongest result as
// the main diagnosis summary.
func aggregate(ctx *DiagnosticContext, results []*AnalyzeResult) *Report {
	best := selectBestResult(results)
	primary := rootCauseFactorFromResult(best, "primary")
	contributing := contributingFactors(results, best)

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

	confidence := adjustedConfidence(best.ConfidenceScore, contributing)
	riskLevel := adjustedRiskLevel(best.RiskLevel, contributing)

	return &Report{
		Namespace:           ctx.Namespace,
		PodName:             ctx.PodName,
		FaultType:           faultType,
		RootCauseSummary:    compositeRootCauseSummary(primary, contributing),
		ConfidenceScore:     confidence,
		Evidences:           evidences,
		ImpactAnalysis:      best.ImpactAnalysis,
		SuggestedActions:    uniqueStrings(actions),
		RiskLevel:           riskLevel,
		NeedHumanConfirm:    true,
		PrimaryRootCause:    &primary,
		ContributingFactors: contributing,
	}
}

// adjustedConfidence lowers the aggregate confidence when high-confidence
// competing or co-causal factors exist, reflecting diagnostic uncertainty.
func adjustedConfidence(bestConfidence float64, contributing []RootCauseFactor) float64 {
	for _, factor := range contributing {
		if factor.ContributingRole == "co-causal" {
			// Two strong candidates — reduce confidence by 10%.
			return bestConfidence * 0.90
		}
		if factor.ContributingRole == "competing" {
			// Strong competitor — reduce confidence by 5%.
			return bestConfidence * 0.95
		}
	}
	return bestConfidence
}

// adjustedRiskLevel escalates the risk when co-causal or competing factors
// indicate a composite fault scenario.
func adjustedRiskLevel(baseRisk string, contributing []RootCauseFactor) string {
	hasCoCausal := false
	competingCount := 0
	for _, factor := range contributing {
		switch factor.ContributingRole {
		case "co-causal":
			hasCoCausal = true
		case "competing":
			competingCount++
		}
	}
	if hasCoCausal {
		return escalateRisk(baseRisk)
	}
	if competingCount >= 2 {
		return escalateRisk(baseRisk)
	}
	return baseRisk
}

// escalateRisk raises the risk level by one notch.
func escalateRisk(risk string) string {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "low":
		return "medium"
	case "medium":
		return "high"
	default:
		return risk
	}
}

func rootCauseFactorFromResult(result *AnalyzeResult, role string) RootCauseFactor {
	if result == nil {
		return RootCauseFactor{}
	}
	refs := make([]string, 0, len(result.Evidences))
	for i, evidence := range result.Evidences {
		refs = append(refs, evidenceRef(evidence, i))
	}
	return RootCauseFactor{
		AnalyzerName:     result.AnalyzerName,
		FaultType:        result.FaultType,
		Summary:          result.RootCauseSummary,
		ConfidenceScore:  result.ConfidenceScore,
		EvidenceRefs:     refs,
		ContributingRole: role,
	}
}

func contributingFactors(results []*AnalyzeResult, primary *AnalyzeResult) []RootCauseFactor {
	factors := []RootCauseFactor{}
	for _, result := range results {
		if result == nil || result == primary {
			continue
		}
		role := classifyContributingRole(result, primary)
		factors = append(factors, rootCauseFactorFromResult(result, role))
	}
	return factors
}

// classifyContributingRole assigns a semantic role label based on the
// relationship between a contributing result and the primary result.
func classifyContributingRole(result, primary *AnalyzeResult) string {
	if primary == nil {
		return "supporting"
	}
	margin := primary.ConfidenceScore - result.ConfidenceScore
	switch {
	case result.ConfidenceScore >= 0.7 && margin < 0.15:
		// High confidence and close to primary — likely co-causal.
		return "co-causal"
	case result.ConfidenceScore >= 0.7:
		// High confidence but clearly behind primary.
		return "competing"
	case result.ConfidenceScore >= 0.5:
		// Moderate confidence — secondary factor.
		return "secondary"
	default:
		return "supporting"
	}
}

func compositeRootCauseSummary(primary RootCauseFactor, contributing []RootCauseFactor) string {
	if len(contributing) == 0 {
		return primary.Summary
	}
	parts := []string{"Primary root cause: " + primary.Summary}
	for _, factor := range contributing {
		parts = append(parts, "Contributing factor: "+factor.Summary)
	}
	return strings.Join(parts, "\n")
}

func evidenceRef(record EvidenceRecord, index int) string {
	source := strings.TrimSpace(record.SourceType)
	if source == "" {
		source = "evidence"
	}
	title := strings.TrimSpace(record.Title)
	if title == "" {
		title = strconv.Itoa(index + 1)
	}
	return source + ":" + title
}

func selectBestResult(results []*AnalyzeResult) *AnalyzeResult {
	best := results[0]
	for _, result := range results[1:] {
		if preferResult(result, best) {
			best = result
		}
	}
	return best
}

func preferResult(candidate, current *AnalyzeResult) bool {
	if current == nil {
		return candidate != nil
	}
	if candidate == nil {
		return false
	}
	// Preserve the strong-OOM-vs-Pending hard override: strong OOM termination
	// evidence always beats Pending, regardless of confidence.
	candidateStrongOOM := hasStrongOOMTerminationEvidence(candidate)
	currentStrongOOM := hasStrongOOMTerminationEvidence(current)
	if candidateStrongOOM && faultTypeMatchesAny(current.FaultType, "PodPending") {
		return true
	}
	if currentStrongOOM && faultTypeMatchesAny(candidate.FaultType, "PodPending") {
		return false
	}
	// Primary comparison: confidence score.
	if candidate.ConfidenceScore != current.ConfidenceScore {
		return candidate.ConfidenceScore > current.ConfidenceScore
	}
	// Tiebreaker: use weighted score incorporating evidence strength and fault
	// severity when confidence scores are equal.
	return weightedResultScore(candidate) > weightedResultScore(current)
}

// weightedResultScore computes a composite score from confidence, fault severity,
// and evidence strength so that primary selection is not dominated by a single
// dimension. Confidence carries the most weight because the rule engine already
// encodes domain knowledge into confidence scores; severity and evidence provide
// tie-breaking and bonus signals.
func weightedResultScore(result *AnalyzeResult) float64 {
	if result == nil {
		return 0
	}
	confidence := result.ConfidenceScore
	severity := faultSeverityWeight(result.FaultType)
	evidence := evidenceStrength(result.Evidences)
	// Strong OOM termination evidence gets an additional boost to preserve the
	// existing OOM-vs-Pending special-case semantics.
	if hasStrongOOMTerminationEvidence(result) {
		evidence += 0.15
	}
	return confidence*0.85 + evidence*0.10 + severity*0.05
}

// faultSeverityWeight maps fault types to a severity weight in [0, 1].
// Higher weight means the fault is more impactful or actionable.
func faultSeverityWeight(faultType string) float64 {
	ft := normalizeFaultName(faultType)
	switch {
	case strings.Contains(ft, "oomkilled"):
		return 1.0
	case strings.Contains(ft, "crashloopbackoff"):
		return 0.9
	case strings.Contains(ft, "probefailed") || strings.Contains(ft, "probefail"):
		return 0.7
	case strings.Contains(ft, "imagepullbackoff") || strings.Contains(ft, "imagepull"):
		return 0.6
	case strings.Contains(ft, "pending"):
		return 0.5
	default:
		return 0.4
	}
}

// evidenceStrength returns a score in [0, 1] reflecting the quantity and
// severity of evidence records attached to an analysis result.
func evidenceStrength(evidences []EvidenceRecord) float64 {
	if len(evidences) == 0 {
		return 0
	}
	warnings := 0
	for _, ev := range evidences {
		if ev.Severity == "warning" || ev.Severity == "critical" {
			warnings++
		}
	}
	// Base score from evidence count (capped at 5), plus bonus for warning-level evidence.
	base := float64(len(evidences)) / 5.0
	if base > 1.0 {
		base = 1.0
	}
	bonus := float64(warnings) / float64(len(evidences)) * 0.3
	return base*0.7 + bonus
}

func hasStrongOOMTerminationEvidence(result *AnalyzeResult) bool {
	if result == nil || !faultTypeMatchesAny(result.FaultType, "OOMKilled") {
		return false
	}
	for _, evidence := range result.Evidences {
		if evidence.SourceType != "k8s_pod_status" {
			continue
		}
		text := strings.ToLower(evidence.Title + " " + evidence.Content)
		if strings.Contains(text, "oom termination evidence") &&
			(strings.Contains(text, "reason=oomkilled") || strings.Contains(text, "exitcode=137")) {
			return true
		}
	}
	return false
}

func faultTypeMatchesAny(actual, expected string) bool {
	expected = normalizeFaultName(expected)
	for _, part := range strings.Split(actual, ",") {
		if normalizeFaultName(part) == expected {
			return true
		}
	}
	return normalizeFaultName(actual) == expected
}

func normalizeFaultName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
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

func enrichReportWithCorrelation(ctx *DiagnosticContext, report *Report) {
	if ctx == nil || report == nil || ctx.Correlations == nil {
		return
	}
	report.Evidences = append(report.Evidences, EvidenceRecord{
		SourceType: "correlation_evidence",
		Title:      "Related pod failures on node, workload, and namespace",
		Content:    correlationEvidenceContent(ctx.Correlations),
		Severity:   correlationSeverity(ctx.Correlations),
		Raw:        ctx.Correlations,
		Timestamp:  time.Now(),
	})
	impact := correlationImpact(ctx.Correlations)
	if impact == "" {
		return
	}
	if strings.TrimSpace(report.ImpactAnalysis) == "" {
		report.ImpactAnalysis = impact
		return
	}
	report.ImpactAnalysis += "\n" + impact
}

func enrichReportWithMetricTrends(ctx *DiagnosticContext, report *Report) {
	if ctx == nil || report == nil || len(ctx.MetricTrends) == 0 {
		return
	}
	critical := false
	parts := make([]string, 0, len(ctx.MetricTrends))
	for _, trend := range ctx.MetricTrends {
		parts = append(parts, fmt.Sprintf("%s/%s=%s growthRatio=%.2f slopePerMinute=%.2f samples=%d",
			trend.Profile,
			trend.Window,
			trend.Classification,
			trend.GrowthRatio,
			trend.SlopePerMinute,
			trend.SampleCount,
		))
		if trend.Classification == "progressive_growth" || trend.Classification == "sudden_spike" || trend.Classification == "restart_increasing" {
			critical = true
		}
	}
	severity := "info"
	if critical {
		severity = "warning"
	}
	report.Evidences = append(report.Evidences, EvidenceRecord{
		SourceType: "prometheus_trend",
		Title:      "Prometheus 1h/6h metric trend analysis",
		Content:    strings.Join(parts, " "),
		Severity:   severity,
		Raw:        ctx.MetricTrends,
		Timestamp:  time.Now(),
	})
	if critical {
		if strings.TrimSpace(report.ImpactAnalysis) != "" {
			report.ImpactAnalysis += "\n"
		}
		report.ImpactAnalysis += "Prometheus trend evidence indicates progressive growth, a sudden spike, or increasing restarts; this diagnosis uses recent behavior rather than only the current snapshot."
	}
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

func correlationEvidenceContent(info *CorrelationInfo) string {
	if info == nil {
		return ""
	}
	parts := []string{
		"node=" + info.NodeName,
		"deployment=" + info.DeploymentName,
		"namespace=" + info.Namespace,
		"nodeAbnormal=" + itoa(info.NodeAbnormalCount),
		"peerAbnormal=" + itoa(info.PeerAbnormalCount),
		"namespaceHot=" + itoa(info.NamespaceHotCount),
	}
	return strings.Join(parts, " ")
}

func correlationSeverity(info *CorrelationInfo) string {
	if info == nil {
		return "info"
	}
	if info.NodeAbnormalCount > 0 || info.PeerAbnormalCount > 0 || info.NamespaceHotCount >= 3 {
		return "warning"
	}
	return "info"
}

func correlationImpact(info *CorrelationInfo) string {
	if info == nil {
		return ""
	}
	parts := []string{}
	if info.NodeAbnormalCount > 0 {
		parts = append(parts, "Other abnormal Pods exist on the same Node; node-level resource, runtime, or network issues should be considered.")
	}
	if info.PeerAbnormalCount > 0 {
		parts = append(parts, "Other Pods in the same Deployment are abnormal; this is likely workload-wide rather than a single-Pod incident.")
	}
	if info.NamespaceHotCount >= 3 {
		parts = append(parts, "Multiple Pods in the Namespace are abnormal or restarting; check shared dependencies, quota, policy, or recent rollout scope.")
	}
	return strings.Join(parts, "\n")
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
