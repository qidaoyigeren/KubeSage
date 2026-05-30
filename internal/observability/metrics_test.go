package observability

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

// TestRenderPrometheus verifies core metric families are exposed in Prometheus
// text format.
func TestRenderPrometheus(t *testing.T) {
	IncDiagnosisTotal("OOMKilled", "success")
	ObserveDiagnosisStage("snapshot", 150*time.Millisecond)
	IncLLMCallTotal("failed")
	IncAnalyzerMatchTotal("oomkilled")

	metrics, err := prometheusText()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`diagnosis_total{fault_type="OOMKilled",status="success"}`,
		`diagnosis_duration_seconds_bucket{stage="snapshot"`,
		`llm_call_total{status="failed"}`,
		`analyzer_match_total{analyzer="oomkilled"}`,
	} {
		if !strings.Contains(metrics, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, metrics)
		}
	}
}

// prometheusText gathers registered metrics into the text exposition format.
func prometheusText() (string, error) {
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	encoder := expfmt.NewEncoder(&builder, expfmt.FmtText)
	for _, family := range families {
		if err := encoder.Encode(family); err != nil {
			return "", err
		}
	}
	return builder.String(), nil
}
