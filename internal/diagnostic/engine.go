package diagnostic

import (
	"strings"
	"time"
)

type DiagnosisEngine struct {
	analyzers []Analyzer
}

func NewDiagnosisEngine(analyzers ...Analyzer) *DiagnosisEngine {
	return &DiagnosisEngine{analyzers: analyzers}
}

func (e *DiagnosisEngine) Diagnose(ctx *DiagnosticContext) (*Report, error) {
	var results []*AnalyzeResult
	for _, analyzer := range e.analyzers {
		if analyzer.Match(ctx) {
			result, err := analyzer.Analyze(ctx)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
	}

	if len(results) == 0 {
		return &Report{
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
		}, nil
	}

	return aggregate(ctx, results), nil
}

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
