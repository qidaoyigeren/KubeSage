package analyzer

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
	"kubesage/internal/prometheus"

	corev1 "k8s.io/api/core/v1"
)

const (
	// OOM memory RCA uses the Kubernetes termination timestamp as the anchor
	// and inspects memory pressure five minutes before and after that moment.
	oomMetricWindow              = 5 * time.Minute
	memoryNearLimitThreshold     = 0.90
	sustainedNearLimitSampleRate = 0.80
	memoryHighBaselineThreshold  = 0.75
	memoryLowRestartThreshold    = 0.60
	memoryLeakGrowthThreshold    = 0.35
	memoryLeakSlopePerMinute     = 0.03
	memoryStableSlopePerMinute   = 0.03
	memoryRestartReturnThreshold = 0.70
)

const (
	memoryPatternLimitTooLow       = "memory_limit_too_low"
	memoryPatternApplicationLeak   = "application_memory_leak"
	memoryPatternSustainedNear     = "sustained_near_limit"
	memoryPatternNearLimitSpike    = "near_limit_spike"
	memoryPatternNoLimitPressure   = "no_limit_pressure"
	memoryPatternInsufficientShape = "insufficient_shape"
)

type OOMKilledAnalyzer struct {
	prometheusClient *prometheus.Client
}

// NewOOMKilledAnalyzer creates the OOMKilled analyzer and optionally wires a
// Prometheus client for memory metric enrichment.
func NewOOMKilledAnalyzer(clients ...*prometheus.Client) *OOMKilledAnalyzer {
	var client *prometheus.Client
	if len(clients) > 0 {
		client = clients[0]
	}
	return &OOMKilledAnalyzer{prometheusClient: client}
}

// Name returns the analyzer identifier used in reports.
func (a *OOMKilledAnalyzer) Name() string { return "oomkilled" }

func (a *OOMKilledAnalyzer) Metadata() diagnostic.AnalyzerMetadata {
	return diagnostic.AnalyzerMetadata{
		Name:             a.Name(),
		FaultType:        "OOMKilled",
		Priority:         100,
		MatchSignals:     []string{"lastState.terminated.reason=OOMKilled", "exitCode=137", "event.message contains oom"},
		RequiredEvidence: []string{"k8s_pod_status", "prometheus", "k8s_key_log", "k8s_topology", "correlation_evidence"},
	}
}

// Match decides whether the current pod snapshot looks like an OOMKilled case.
func (a *OOMKilledAnalyzer) Match(ctx *diagnostic.DiagnosticContext) bool {
	if ctx.Pod == nil {
		return false
	}
	oomSignal := hasOOMContextSignal(ctx)
	for _, status := range ctx.Pod.Status.ContainerStatuses {
		if terminationIndicatesOOM(status.LastTerminationState.Terminated, oomSignal) ||
			terminationIndicatesOOM(status.State.Terminated, oomSignal) {
			return true
		}
	}
	for _, event := range ctx.Events {
		if strings.Contains(strings.ToLower(event.Message), "oomkilled") {
			return true
		}
	}
	return false
}

func terminationIndicatesOOM(terminated *corev1.ContainerStateTerminated, hasOOMSignal bool) bool {
	if terminated == nil {
		return false
	}
	if strings.EqualFold(terminated.Reason, "OOMKilled") {
		return true
	}
	return terminated.ExitCode == 137 && hasOOMSignal
}

func hasOOMContextSignal(ctx *diagnostic.DiagnosticContext) bool {
	if ctx == nil {
		return false
	}
	for _, event := range ctx.Events {
		if strings.Contains(strings.ToLower(event.Reason+" "+event.Message), "oom") {
			return true
		}
	}
	for _, logs := range ctx.Logs {
		if containsAny(logs.Current+"\n"+logs.Previous+"\n"+logs.Loki, oomKilledLogKeywords) {
			return true
		}
	}
	return false
}

// Analyze collects Kubernetes, log, event, and optional Prometheus evidence for
// an OOMKilled diagnosis.
func (a *OOMKilledAnalyzer) Analyze(ctx *diagnostic.DiagnosticContext) (*diagnostic.AnalyzeResult, error) {
	evidences := []diagnostic.EvidenceRecord{}

	// Always record the configured requests/limits first. Even when Prometheus
	// is unavailable, this gives the report enough context for manual triage.
	for _, c := range ctx.Pod.Spec.Containers {
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "Container memory requests and limits",
			Content:    fmt.Sprintf("container=%s memoryRequest=%s memoryLimit=%s", c.Name, memoryRequestString(c), memoryLimitString(c)),
			Severity:   "warning",
			Raw:        c.Resources,
			Timestamp:  time.Now(),
		})
	}

	// Only lastState.terminated carries the precise finishedAt timestamp needed
	// for the Prometheus window. Event-only OOM matches are still reported, but
	// metric enrichment is skipped with a warning evidence below.
	terminations := oomTerminations(ctx.Pod)
	for _, termination := range terminations {
		timestamp := time.Now()
		if !termination.FinishedAt.IsZero() {
			timestamp = termination.FinishedAt
		}
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "k8s_pod_status",
			Title:      "OOM termination evidence",
			Content:    fmt.Sprintf("container=%s restartCount=%d reason=%s exitCode=%d finishedAt=%s", termination.ContainerName, termination.RestartCount, termination.Reason, termination.ExitCode, formatOptionalTime(termination.FinishedAt)),
			Severity:   "critical",
			Raw:        termination.RawStatus,
			Timestamp:  timestamp,
		})
	}
	if len(terminations) == 0 && ctx.MetricsEnabled {
		evidences = append(evidences, diagnostic.EvidenceRecord{
			SourceType: "prometheus",
			Title:      "Prometheus memory query skipped",
			Content:    "OOMKilled was matched from events, but pod status has no lastState.terminated record with finishedAt; memory metrics need lastState.terminated.finishedAt as the fault time.",
			Severity:   "warning",
			Raw:        ctx.Pod.Status.ContainerStatuses,
			Timestamp:  time.Now(),
		})
	}

	for _, event := range ctx.Events {
		if strings.Contains(strings.ToLower(event.Reason+" "+event.Message), "oom") {
			evidences = append(evidences, eventEvidence(event, "warning"))
		}
	}

	for _, logs := range ctx.Logs {
		if logs.Previous != "" {
			evidences = append(evidences, diagnostic.EvidenceRecord{
				SourceType: "k8s_log",
				Title:      "Previous logs before OOMKilled",
				Content:    trimLog(logs.ContainerName, logs.Previous, logs.Current, logs.Loki),
				Severity:   "info",
				Raw:        logs,
				Timestamp:  time.Now(),
			})
		}
	}
	evidences = append(evidences, keyLogEvidences(ctx.Logs, "Key OOMKilled log fragments", oomKilledLogKeywords, "critical")...)

	sustainedNearLimit := false
	limitTooLowPattern := false
	memoryLeakPattern := false
	if ctx.MetricsEnabled {
		// Metric enrichment is best-effort: every Prometheus failure path returns
		// warning evidence instead of failing the analyzer.
		for _, termination := range terminations {
			container, ok := findContainer(ctx.Pod, termination.ContainerName)
			limitBytes, limitSet := memoryLimitBytes(container, ok)
			evidence, analysis := a.queryMemoryEvidence(ctx, termination, limitBytes, limitSet)
			evidences = append(evidences, evidence)
			if analysis != nil {
				if analysis.SustainedNearLimit {
					sustainedNearLimit = true
				}
				if analysis.LimitTooLowPattern {
					limitTooLowPattern = true
				}
				if analysis.ApplicationLeakPattern {
					memoryLeakPattern = true
				}
			}
		}
	}

	summary := "OOMKilled exitCode 137 memory limit out of memory: 容器最近一次退出由 OOMKilled 或退出码 137 触发，更倾向于内存 memory limit 不足、应用 heap 内存持续增长或突发内存分配。"
	confidence := 0.82
	actions := []string{
		"检查应用内存泄漏、大对象缓存、批量查询、JVM/Go heap 参数和近期发布变更。",
		"结合 Prometheus 中 container_memory_working_set_bytes 与 memory limit 的比例确认是否长期贴近上限。",
		"临时提高 memory limit 可缓解风险，但需要标记为临时方案并继续根因排查。",
	}
	switch {
	case memoryLeakPattern:
		summary = "OOMKilled exitCode 137 application memory leak: Prometheus 内存曲线显示启动后从低水位持续爬升到 memory limit，或 OOM 重启后从低点重新增长，更倾向于应用内存泄漏或未释放缓存。"
		actions = append(actions,
			"优先抓取 heap/profile、对象分配热点和最近发布差异，确认是否存在持续增长的应用内存泄漏。",
			"提高 memory limit 只能作为临时止血，需要同步设置增长率告警并验证重启后曲线是否再次爬升。",
		)
	case limitTooLowPattern:
		summary = "OOMKilled exitCode 137 memory limit too low: Prometheus 内存曲线 baseline 已长期接近 memory limit，OOM 前斜率较低且重启后快速回到同一高水位，更倾向于 limit 配置低于工作负载正常基线。"
		actions = append(actions,
			"按正常运行 baseline 重新评估 memory request/limit，并检查是否存在固定缓存或启动即加载的大对象。",
			"调整 limit 后继续观察 baseline 与 limit 的间隔，而不只看 OOM 是否消失。",
		)
	case sustainedNearLimit:
		summary = "OOMKilled exitCode 137 memory limit: Prometheus 内存曲线 working set 持续接近容器 memory limit，但曲线形态不足以区分 limit 配太低还是应用内存泄漏。"
		actions = append(actions, "优先降低峰值内存或提高 memory limit，并为关键容器补充内存使用率告警。")
	}
	confidence = oomConfidence(terminations, evidences, sustainedNearLimit, limitTooLowPattern, memoryLeakPattern, ctx.MetricTrends)

	return &diagnostic.AnalyzeResult{
		AnalyzerName:     a.Name(),
		FaultType:        "OOMKilled",
		RootCauseSummary: summary,
		ConfidenceScore:  confidence,
		Evidences:        evidences,
		ImpactAnalysis:   "容器被 kubelet 杀死并重启，可能造成请求中断、队列堆积或批处理失败。",
		SuggestedActions: actions,
		RiskLevel:        "high",
		NeedHumanConfirm: true,
	}, nil
}

// oomConfidence adjusts confidence according to termination, log, and metric
// evidence strength instead of relying on a single hard-coded value.
func oomConfidence(terminations []oomTermination, evidences []diagnostic.EvidenceRecord, sustainedNearLimit, limitTooLowPattern, memoryLeakPattern bool, trends []diagnostic.MetricTrend) float64 {
	score := 0.72
	if len(terminations) > 0 {
		score += 0.10
	}
	if hasEvidence(evidences, "k8s_key_log", "") {
		score += 0.03
	}
	if hasEvidence(evidences, "prometheus", "working set") {
		score += 0.05
	}
	if sustainedNearLimit {
		score += 0.03
	}
	if limitTooLowPattern || memoryLeakPattern {
		score += 0.06
	}
	// Boost confidence when metric trends show progressive memory growth.
	for _, trend := range trends {
		if strings.Contains(strings.ToLower(trend.Metric), "memory") {
			switch trend.Classification {
			case "progressive_growth":
				score += 0.08
			case "sudden_spike":
				score += 0.04
			}
		}
	}
	return clampConfidence(score)
}

// queryMemoryEvidence queries Prometheus around one OOM termination and returns
// either metric evidence or warning evidence when metrics are unavailable.
func (a *OOMKilledAnalyzer) queryMemoryEvidence(ctx *diagnostic.DiagnosticContext, termination oomTermination, limitBytes int64, limitSet bool) (diagnostic.EvidenceRecord, *memoryPressureAnalysis) {
	if termination.FinishedAt.IsZero() {
		return diagnostic.EvidenceRecord{
			SourceType: "prometheus",
			Title:      "Prometheus memory query skipped",
			Content:    fmt.Sprintf("container=%s lastState.terminated.finishedAt is empty; skipped container_memory_working_set_bytes query.", termination.ContainerName),
			Severity:   "warning",
			Raw:        termination,
			Timestamp:  time.Now(),
		}, nil
	}
	start := termination.FinishedAt.Add(-oomMetricWindow)
	end := termination.FinishedAt.Add(oomMetricWindow)

	// Prometheus is optional in KubeSage. Preserve that absence as evidence so
	// the report explains why no memory curve was used.
	if a.prometheusClient == nil || !a.prometheusClient.Configured() {
		return diagnostic.EvidenceRecord{
			SourceType: "prometheus",
			Title:      "Prometheus unavailable",
			Content:    fmt.Sprintf("prometheus.base_url is not configured; skipped container_memory_working_set_bytes query for container=%s window=%s..%s.", termination.ContainerName, start.Format(time.RFC3339), end.Format(time.RFC3339)),
			Severity:   "warning",
			Raw: prometheusMemoryEvidence{
				ContainerName:  termination.ContainerName,
				FaultTime:      termination.FinishedAt,
				WindowStart:    start,
				WindowEnd:      end,
				MemoryLimitSet: limitSet,
				MemoryLimit:    limitBytes,
			},
			Timestamp: time.Now(),
		}, nil
	}

	queryCtx := context.Background()
	if ctx.RequestContext != nil {
		queryCtx = ctx.RequestContext
	}
	result, err := a.prometheusClient.QueryMemoryWorkingSetBytes(queryCtx, ctx.Namespace, ctx.PodName, termination.ContainerName, start, end)
	if err != nil {
		return diagnostic.EvidenceRecord{
			SourceType: "prometheus",
			Title:      "Prometheus memory query failed",
			Content:    fmt.Sprintf("query container_memory_working_set_bytes failed for container=%s window=%s..%s: %v", termination.ContainerName, start.Format(time.RFC3339), end.Format(time.RFC3339), err),
			Severity:   "warning",
			Raw: prometheusMemoryEvidence{
				ContainerName:  termination.ContainerName,
				FaultTime:      termination.FinishedAt,
				WindowStart:    start,
				WindowEnd:      end,
				MemoryLimitSet: limitSet,
				MemoryLimit:    limitBytes,
				Error:          err.Error(),
			},
			Timestamp: time.Now(),
		}, nil
	}

	points := result.AllPoints()
	if len(points) == 0 {
		return diagnostic.EvidenceRecord{
			SourceType: "prometheus",
			Title:      "Prometheus memory metrics empty",
			Content:    fmt.Sprintf("no container_memory_working_set_bytes samples found for container=%s window=%s..%s.", termination.ContainerName, start.Format(time.RFC3339), end.Format(time.RFC3339)),
			Severity:   "warning",
			Raw: prometheusMemoryEvidence{
				ContainerName:  termination.ContainerName,
				FaultTime:      termination.FinishedAt,
				WindowStart:    start,
				WindowEnd:      end,
				MemoryLimitSet: limitSet,
				MemoryLimit:    limitBytes,
				QueryResult:    result,
			},
			Timestamp: time.Now(),
		}, nil
	}

	analysis := analyzeMemoryPressure(points, limitBytes, limitSet, termination.FinishedAt)
	severity := "info"
	if !limitSet {
		severity = "warning"
	}
	if analysis.SustainedNearLimit || analysis.LimitTooLowPattern || analysis.ApplicationLeakPattern {
		severity = "critical"
	}
	content := memoryEvidenceContent(termination.ContainerName, start, end, analysis, limitSet)
	return diagnostic.EvidenceRecord{
		SourceType: "prometheus",
		Title:      "Container memory working set around OOMKilled",
		Content:    content,
		Severity:   severity,
		Raw: prometheusMemoryEvidence{
			ContainerName:  termination.ContainerName,
			FaultTime:      termination.FinishedAt,
			WindowStart:    start,
			WindowEnd:      end,
			MemoryLimitSet: limitSet,
			MemoryLimit:    limitBytes,
			Analysis:       analysis,
			QueryResult:    result,
		},
		Timestamp: time.Now(),
	}, &analysis
}

type oomTermination struct {
	ContainerName string                 `json:"container_name"`
	RestartCount  int32                  `json:"restart_count"`
	Reason        string                 `json:"reason"`
	ExitCode      int32                  `json:"exit_code"`
	FinishedAt    time.Time              `json:"finished_at"`
	RawStatus     corev1.ContainerStatus `json:"raw_status"`
}

type prometheusMemoryEvidence struct {
	ContainerName  string                       `json:"container_name"`
	FaultTime      time.Time                    `json:"fault_time"`
	WindowStart    time.Time                    `json:"window_start"`
	WindowEnd      time.Time                    `json:"window_end"`
	MemoryLimitSet bool                         `json:"memory_limit_set"`
	MemoryLimit    int64                        `json:"memory_limit_bytes"`
	Analysis       memoryPressureAnalysis       `json:"analysis,omitempty"`
	QueryResult    *prometheus.QueryRangeResult `json:"query_result,omitempty"`
	Error          string                       `json:"error,omitempty"`
}

type memoryPressureAnalysis struct {
	SampleCount                int     `json:"sample_count"`
	PreOOMSampleCount          int     `json:"pre_oom_sample_count"`
	PostOOMSampleCount         int     `json:"post_oom_sample_count"`
	MemoryLimitBytes           int64   `json:"memory_limit_bytes"`
	MaxWorkingSetBytes         float64 `json:"max_working_set_bytes"`
	MinWorkingSetBytes         float64 `json:"min_working_set_bytes"`
	AvgWorkingSetBytes         float64 `json:"avg_working_set_bytes"`
	MaxLimitRatio              float64 `json:"max_limit_ratio"`
	AvgLimitRatio              float64 `json:"avg_limit_ratio"`
	BaselineLimitRatio         float64 `json:"baseline_limit_ratio"`
	PreOOMEndLimitRatio        float64 `json:"pre_oom_end_limit_ratio"`
	PreOOMGrowthRatio          float64 `json:"pre_oom_growth_ratio"`
	PreOOMSlopeRatioPerMinute  float64 `json:"pre_oom_slope_ratio_per_minute"`
	PostRestartMinLimitRatio   float64 `json:"post_restart_min_limit_ratio"`
	PostRestartMaxLimitRatio   float64 `json:"post_restart_max_limit_ratio"`
	PostRestartFirstLimitRatio float64 `json:"post_restart_first_limit_ratio"`
	PostRestartLastLimitRatio  float64 `json:"post_restart_last_limit_ratio"`
	PostRestartGrowthRatio     float64 `json:"post_restart_growth_ratio"`
	NearLimitThreshold         float64 `json:"near_limit_threshold"`
	NearLimitSamples           int     `json:"near_limit_samples"`
	SustainedSampleRate        float64 `json:"sustained_sample_rate"`
	SustainedNearLimit         bool    `json:"sustained_near_limit"`
	MemoryPattern              string  `json:"memory_pattern"`
	PatternReason              string  `json:"pattern_reason"`
	LimitTooLowPattern         bool    `json:"limit_too_low_pattern"`
	ApplicationLeakPattern     bool    `json:"application_leak_pattern"`
}

// oomTerminations extracts the OOM-related lastState.terminated records from a
// pod status snapshot.
func oomTerminations(pod *corev1.Pod) []oomTermination {
	if pod == nil {
		return nil
	}
	terminations := make([]oomTermination, 0)
	for _, status := range pod.Status.ContainerStatuses {
		terminated := status.LastTerminationState.Terminated
		if terminated == nil {
			continue
		}
		if terminated.Reason != "OOMKilled" && terminated.ExitCode != 137 {
			continue
		}
		terminations = append(terminations, oomTermination{
			ContainerName: status.Name,
			RestartCount:  status.RestartCount,
			Reason:        terminated.Reason,
			ExitCode:      terminated.ExitCode,
			FinishedAt:    terminated.FinishedAt.Time,
			RawStatus:     status,
		})
	}
	return terminations
}

// findContainer locates the pod spec container that matches a container status.
func findContainer(pod *corev1.Pod, name string) (corev1.Container, bool) {
	if pod == nil {
		return corev1.Container{}, false
	}
	for _, container := range pod.Spec.Containers {
		if container.Name == name {
			return container, true
		}
	}
	return corev1.Container{}, false
}

// memoryRequestString formats a container memory request for human-readable
// evidence, using <unset> when the request is absent.
func memoryRequestString(container corev1.Container) string {
	quantity, ok := container.Resources.Requests[corev1.ResourceMemory]
	if !ok || quantity.Value() <= 0 {
		return "<unset>"
	}
	return quantity.String()
}

// memoryLimitString formats a container memory limit for human-readable
// evidence, using <unset> when the limit is absent.
func memoryLimitString(container corev1.Container) string {
	quantity, ok := container.Resources.Limits[corev1.ResourceMemory]
	if !ok || quantity.Value() <= 0 {
		return "<unset>"
	}
	return quantity.String()
}

// memoryLimitBytes returns the numeric memory limit used for ratio analysis.
func memoryLimitBytes(container corev1.Container, containerFound bool) (int64, bool) {
	if !containerFound {
		return 0, false
	}
	quantity, ok := container.Resources.Limits[corev1.ResourceMemory]
	if !ok || quantity.Value() <= 0 {
		return 0, false
	}
	return quantity.Value(), true
}

// analyzeMemoryPressure keeps the old near-limit count, but separates it from
// root-cause shape. Limit-too-low needs high baseline and low slope; leak needs
// low startup/restart values followed by sustained growth toward the limit.
func analyzeMemoryPressure(points []prometheus.Point, limitBytes int64, limitSet bool, faultTime time.Time) memoryPressureAnalysis {
	points = sortedPoints(points)
	analysis := memoryPressureAnalysis{
		SampleCount:         len(points),
		MemoryLimitBytes:    limitBytes,
		NearLimitThreshold:  memoryNearLimitThreshold,
		SustainedSampleRate: sustainedNearLimitSampleRate,
		MinWorkingSetBytes:  math.MaxFloat64,
		MemoryPattern:       memoryPatternInsufficientShape,
	}
	var sum float64
	for _, point := range points {
		value := point.Value
		sum += value
		if value > analysis.MaxWorkingSetBytes {
			analysis.MaxWorkingSetBytes = value
		}
		if value < analysis.MinWorkingSetBytes {
			analysis.MinWorkingSetBytes = value
		}
		if limitSet {
			ratio := value / float64(limitBytes)
			analysis.AvgLimitRatio += ratio
			if ratio > analysis.MaxLimitRatio {
				analysis.MaxLimitRatio = ratio
			}
			if ratio >= memoryNearLimitThreshold {
				analysis.NearLimitSamples++
			}
		}
	}
	if len(points) > 0 {
		analysis.AvgWorkingSetBytes = sum / float64(len(points))
	}
	if analysis.MinWorkingSetBytes == math.MaxFloat64 {
		analysis.MinWorkingSetBytes = 0
	}
	if limitSet && len(points) > 0 {
		analysis.AvgLimitRatio = analysis.AvgLimitRatio / float64(len(points))
		nearLimitRate := float64(analysis.NearLimitSamples) / float64(len(points))
		analysis.SustainedNearLimit = len(points) >= 3 && nearLimitRate >= sustainedNearLimitSampleRate
		enrichMemoryShape(&analysis, points, faultTime, limitBytes)
	}
	return analysis
}

func sortedPoints(points []prometheus.Point) []prometheus.Point {
	out := append([]prometheus.Point(nil), points...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out
}

func enrichMemoryShape(analysis *memoryPressureAnalysis, points []prometheus.Point, faultTime time.Time, limitBytes int64) {
	if analysis == nil || len(points) == 0 || limitBytes <= 0 {
		return
	}
	pre, post := splitAroundFault(points, faultTime)
	analysis.PreOOMSampleCount = len(pre)
	analysis.PostOOMSampleCount = len(post)
	if len(pre) == 0 {
		pre = points
		analysis.PreOOMSampleCount = len(pre)
	}

	analysis.BaselineLimitRatio = averageLimitRatio(firstSegment(pre), limitBytes)
	analysis.PreOOMEndLimitRatio = averageLimitRatio(lastSegment(pre), limitBytes)
	if analysis.BaselineLimitRatio > 0 {
		analysis.PreOOMGrowthRatio = (analysis.PreOOMEndLimitRatio - analysis.BaselineLimitRatio) / analysis.BaselineLimitRatio
	}
	analysis.PreOOMSlopeRatioPerMinute = slopeRatioPerMinute(pre, limitBytes)

	if len(post) > 0 {
		analysis.PostRestartMinLimitRatio = minLimitRatio(post, limitBytes)
		analysis.PostRestartMaxLimitRatio = maxLimitRatio(post, limitBytes)
		analysis.PostRestartFirstLimitRatio = averageLimitRatio(firstSegment(post), limitBytes)
		analysis.PostRestartLastLimitRatio = averageLimitRatio(lastSegment(post), limitBytes)
		if analysis.PostRestartFirstLimitRatio > 0 {
			analysis.PostRestartGrowthRatio = (analysis.PostRestartLastLimitRatio - analysis.PostRestartFirstLimitRatio) / analysis.PostRestartFirstLimitRatio
		}
	}

	preGrowsToLimit := len(pre) >= 4 &&
		analysis.BaselineLimitRatio <= memoryHighBaselineThreshold &&
		analysis.PreOOMEndLimitRatio >= memoryNearLimitThreshold &&
		(analysis.PreOOMGrowthRatio >= memoryLeakGrowthThreshold ||
			analysis.PreOOMSlopeRatioPerMinute >= memoryLeakSlopePerMinute)
	postDropsAndRegrows := len(post) >= 3 &&
		analysis.PostRestartMinLimitRatio > 0 &&
		analysis.PostRestartMinLimitRatio <= memoryLowRestartThreshold &&
		analysis.PostRestartMaxLimitRatio >= memoryHighBaselineThreshold &&
		analysis.PostRestartGrowthRatio >= memoryLeakGrowthThreshold
	analysis.ApplicationLeakPattern = preGrowsToLimit || postDropsAndRegrows

	postReturnsHigh := len(post) == 0 ||
		analysis.PostRestartFirstLimitRatio >= memoryRestartReturnThreshold ||
		analysis.PostRestartMaxLimitRatio >= memoryNearLimitThreshold
	analysis.LimitTooLowPattern = analysis.SustainedNearLimit &&
		!analysis.ApplicationLeakPattern &&
		analysis.BaselineLimitRatio >= memoryHighBaselineThreshold &&
		math.Abs(analysis.PreOOMSlopeRatioPerMinute) <= memoryStableSlopePerMinute &&
		postReturnsHigh

	switch {
	case analysis.ApplicationLeakPattern:
		analysis.MemoryPattern = memoryPatternApplicationLeak
		analysis.PatternReason = "low baseline or restart drop followed by positive slope toward memory limit"
	case analysis.LimitTooLowPattern:
		analysis.MemoryPattern = memoryPatternLimitTooLow
		analysis.PatternReason = "high baseline near memory limit with stable pre-OOM slope and fast post-restart return"
	case analysis.SustainedNearLimit:
		analysis.MemoryPattern = memoryPatternSustainedNear
		analysis.PatternReason = "many samples are near memory limit but curve shape is ambiguous"
	case analysis.NearLimitSamples > 0:
		analysis.MemoryPattern = memoryPatternNearLimitSpike
		analysis.PatternReason = "some samples are near memory limit without sustained shape"
	default:
		analysis.MemoryPattern = memoryPatternNoLimitPressure
		analysis.PatternReason = "memory samples do not approach limit"
	}
}

func splitAroundFault(points []prometheus.Point, faultTime time.Time) ([]prometheus.Point, []prometheus.Point) {
	if faultTime.IsZero() {
		return points, nil
	}
	pre := make([]prometheus.Point, 0, len(points))
	post := make([]prometheus.Point, 0, len(points))
	for _, point := range points {
		if point.Timestamp.After(faultTime) {
			post = append(post, point)
			continue
		}
		pre = append(pre, point)
	}
	return pre, post
}

func firstSegment(points []prometheus.Point) []prometheus.Point {
	if len(points) <= 3 {
		return points
	}
	return points[:segmentSize(len(points))]
}

func lastSegment(points []prometheus.Point) []prometheus.Point {
	if len(points) <= 3 {
		return points
	}
	size := segmentSize(len(points))
	return points[len(points)-size:]
}

func segmentSize(n int) int {
	size := n / 3
	if n%3 != 0 {
		size++
	}
	if size < 1 {
		return 1
	}
	return size
}

func averageLimitRatio(points []prometheus.Point, limitBytes int64) float64 {
	if len(points) == 0 || limitBytes <= 0 {
		return 0
	}
	var sum float64
	for _, point := range points {
		sum += point.Value / float64(limitBytes)
	}
	return sum / float64(len(points))
}

func minLimitRatio(points []prometheus.Point, limitBytes int64) float64 {
	if len(points) == 0 || limitBytes <= 0 {
		return 0
	}
	min := math.MaxFloat64
	for _, point := range points {
		ratio := point.Value / float64(limitBytes)
		if ratio < min {
			min = ratio
		}
	}
	if min == math.MaxFloat64 {
		return 0
	}
	return min
}

func maxLimitRatio(points []prometheus.Point, limitBytes int64) float64 {
	if len(points) == 0 || limitBytes <= 0 {
		return 0
	}
	var max float64
	for _, point := range points {
		ratio := point.Value / float64(limitBytes)
		if ratio > max {
			max = ratio
		}
	}
	return max
}

func slopeRatioPerMinute(points []prometheus.Point, limitBytes int64) float64 {
	if len(points) < 2 || limitBytes <= 0 {
		return 0
	}
	first := points[0]
	last := points[len(points)-1]
	minutes := last.Timestamp.Sub(first.Timestamp).Minutes()
	if minutes <= 0 {
		minutes = float64(len(points) - 1)
	}
	return ((last.Value - first.Value) / float64(limitBytes)) / minutes
}

// memoryEvidenceContent builds the short text summary stored in the evidence
// row for the Prometheus memory query.
func memoryEvidenceContent(containerName string, start, end time.Time, analysis memoryPressureAnalysis, limitSet bool) string {
	if !limitSet {
		return fmt.Sprintf("container=%s window=%s..%s samples=%d memoryLimit=<unset> maxWorkingSet=%s avgWorkingSet=%s cannotCompareWithLimit=true",
			containerName,
			start.Format(time.RFC3339),
			end.Format(time.RFC3339),
			analysis.SampleCount,
			formatBytes(analysis.MaxWorkingSetBytes),
			formatBytes(analysis.AvgWorkingSetBytes),
		)
	}
	return fmt.Sprintf("container=%s window=%s..%s samples=%d memoryLimit=%s maxWorkingSet=%s avgWorkingSet=%s maxLimitRatio=%.1f%% avgLimitRatio=%.1f%% nearLimitSamples=%d/%d threshold=%.0f%% sustainedNearLimit=%t memoryPattern=%s baselineLimitRatio=%.1f%% preOOMEndLimitRatio=%.1f%% preOOMSlopeRatioPerMinute=%.2f%% postRestartMinLimitRatio=%.1f%% postRestartMaxLimitRatio=%.1f%% postRestartGrowthRatio=%.1f%% patternReason=%q",
		containerName,
		start.Format(time.RFC3339),
		end.Format(time.RFC3339),
		analysis.SampleCount,
		formatBytes(float64(analysis.MemoryLimitBytes)),
		formatBytes(analysis.MaxWorkingSetBytes),
		formatBytes(analysis.AvgWorkingSetBytes),
		analysis.MaxLimitRatio*100,
		analysis.AvgLimitRatio*100,
		analysis.NearLimitSamples,
		analysis.SampleCount,
		memoryNearLimitThreshold*100,
		analysis.SustainedNearLimit,
		analysis.MemoryPattern,
		analysis.BaselineLimitRatio*100,
		analysis.PreOOMEndLimitRatio*100,
		analysis.PreOOMSlopeRatioPerMinute*100,
		analysis.PostRestartMinLimitRatio*100,
		analysis.PostRestartMaxLimitRatio*100,
		analysis.PostRestartGrowthRatio*100,
		analysis.PatternReason,
	)
}

// formatOptionalTime formats a timestamp and makes zero values explicit.
func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return "<empty>"
	}
	return t.Format(time.RFC3339)
}

// formatBytes renders byte values with binary units for evidence text.
func formatBytes(bytes float64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%.0fB", bytes)
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := bytes
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value = value / 1024
		unit++
	}
	return fmt.Sprintf("%.2f%s", value, units[unit])
}
