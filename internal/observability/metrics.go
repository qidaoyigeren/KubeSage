package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	diagnosisTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diagnosis_total",
			Help: "Total diagnosis tasks by fault type and status.",
		},
		[]string{"fault_type", "status"},
	)
	diagnosisDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "diagnosis_duration_seconds",
			Help:    "Diagnosis pipeline stage duration distribution.",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 12),
		},
		[]string{"stage"},
	)
	llmCallTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llm_call_total",
			Help: "Total LLM calls by status.",
		},
		[]string{"status"},
	)
	analyzerMatchTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "analyzer_match_total",
			Help: "Total analyzer matches by analyzer name.",
		},
		[]string{"analyzer"},
	)
	agentStepTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agent_step_total",
			Help: "Total agent steps by stage, status, and tool.",
		},
		[]string{"stage", "status", "tool"},
	)
	agentToolDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agent_tool_duration_seconds",
			Help:    "Agent tool execution duration distribution.",
			Buckets: prometheus.ExponentialBuckets(0.005, 2, 12),
		},
		[]string{"tool"},
	)
	agentHypothesisUpdatesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agent_hypothesis_updates_total",
			Help: "Total hypothesis updates by type and status.",
		},
		[]string{"hypothesis_type", "status"},
	)
	remediationActionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "remediation_action_total",
			Help: "Total remediation actions by risk level and status.",
		},
		[]string{"risk_level", "status"},
	)
	llmPlannerFallbackTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "llm_planner_fallback_total",
			Help: "Total LLM planner fallback events by operation type.",
		},
		[]string{"operation", "reason"},
	)
)

func init() {
	prometheus.MustRegister(diagnosisTotal, diagnosisDuration, llmCallTotal, analyzerMatchTotal, agentStepTotal, agentToolDuration, agentHypothesisUpdatesTotal, remediationActionTotal, llmPlannerFallbackTotal)
}

// IncLLMPlannerFallback records an LLM planner fallback event.
func IncLLMPlannerFallback(operation, reason string) {
	llmPlannerFallbackTotal.WithLabelValues(operation, reason).Inc()
}

// IncDiagnosisTotal records one completed diagnosis task.
func IncDiagnosisTotal(faultType, status string) {
	diagnosisTotal.WithLabelValues(faultType, status).Inc()
}

// ObserveDiagnosisStage records elapsed time for one diagnosis pipeline stage.
func ObserveDiagnosisStage(stage string, duration time.Duration) {
	diagnosisDuration.WithLabelValues(stage).Observe(duration.Seconds())
}

// IncLLMCallTotal records LLM call success or failure.
func IncLLMCallTotal(status string) {
	llmCallTotal.WithLabelValues(status).Inc()
}

// IncAnalyzerMatchTotal records that one analyzer matched a diagnostic context.
func IncAnalyzerMatchTotal(analyzer string) {
	analyzerMatchTotal.WithLabelValues(analyzer).Inc()
}

// IncAgentStepTotal records one persisted or attempted agent step.
func IncAgentStepTotal(stage, status, tool string) {
	agentStepTotal.WithLabelValues(stage, status, tool).Inc()
}

// ObserveAgentToolDuration records the elapsed time for one agent tool call.
func ObserveAgentToolDuration(tool string, duration time.Duration) {
	agentToolDuration.WithLabelValues(tool).Observe(duration.Seconds())
}

// IncAgentHypothesisUpdatesTotal records a hypothesis state transition.
func IncAgentHypothesisUpdatesTotal(hypothesisType, status string) {
	agentHypothesisUpdatesTotal.WithLabelValues(hypothesisType, status).Inc()
}

// IncRemediationActionTotal records a policy-vetted remediation action.
func IncRemediationActionTotal(riskLevel, status string) {
	remediationActionTotal.WithLabelValues(riskLevel, status).Inc()
}

// Handler exposes all registered Prometheus metrics using client_golang.
func Handler() http.Handler {
	return promhttp.Handler()
}
