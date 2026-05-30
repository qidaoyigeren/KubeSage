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
)

func init() {
	prometheus.MustRegister(diagnosisTotal, diagnosisDuration, llmCallTotal, analyzerMatchTotal)
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

// Handler exposes all registered Prometheus metrics using client_golang.
func Handler() http.Handler {
	return promhttp.Handler()
}
