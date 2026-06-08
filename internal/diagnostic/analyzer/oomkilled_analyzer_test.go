package analyzer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
	"kubesage/internal/prometheus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestOOMKilledAnalyzerAddsPrometheusMemoryEvidence verifies that OOMKilled
// analysis queries the finishedAt window and records critical metric evidence.
func TestOOMKilledAnalyzerAddsPrometheusMemoryEvidence(t *testing.T) {
	finishedAt := time.Unix(1_000, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		query := `container_memory_working_set_bytes{namespace="default",pod="pod-a",container="app"}`
		if got := r.URL.Query().Get("query"); got != query {
			t.Fatalf("unexpected query: %s", got)
		}
		if got := r.URL.Query().Get("start"); got != "700" {
			t.Fatalf("unexpected start: %s", got)
		}
		if got := r.URL.Query().Get("end"); got != "1300" {
			t.Fatalf("unexpected end: %s", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"result": []map[string]interface{}{
					{
						"metric": map[string]string{"container": "app"},
						"values": [][]interface{}{
							{float64(700), "950"},
							{float64(730), "960"},
							{float64(760), "970"},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "pod-a",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: resource.MustParse("1000"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "app",
					RestartCount: 1,
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Reason:     "OOMKilled",
							ExitCode:   137,
							FinishedAt: metav1.NewTime(finishedAt),
						},
					},
				},
			},
		},
	}
	client := prometheus.NewClient(config.PrometheusConfig{BaseURL: server.URL})
	analyzer := NewOOMKilledAnalyzer(client)
	report, err := analyzer.Analyze(&diagnostic.DiagnosticContext{
		RequestContext: context.Background(),
		Namespace:      "default",
		PodName:        "pod-a",
		Pod:            pod,
		MetricsEnabled: true,
	})
	if err != nil {
		t.Fatalf("analyze failed: %v", err)
	}

	found := false
	for _, evidence := range report.Evidences {
		if evidence.SourceType == "prometheus" && evidence.Title == "Container memory working set around OOMKilled" {
			found = true
			if evidence.Severity != "critical" {
				t.Fatalf("expected critical evidence, got %s", evidence.Severity)
			}
			if !strings.Contains(evidence.Content, "sustainedNearLimit=true") {
				t.Fatalf("missing sustained near limit result: %s", evidence.Content)
			}
			if !strings.Contains(evidence.Content, "memoryPattern=memory_limit_too_low") {
				t.Fatalf("missing memory limit pattern: %s", evidence.Content)
			}
		}
	}
	if !found {
		t.Fatal("expected prometheus memory evidence")
	}
}

func TestAnalyzeMemoryPressureClassifiesLimitTooLowShape(t *testing.T) {
	faultTime := time.Unix(1_000, 0)
	points := []prometheus.Point{
		{Timestamp: faultTime.Add(-4 * time.Minute), Value: 920},
		{Timestamp: faultTime.Add(-3 * time.Minute), Value: 930},
		{Timestamp: faultTime.Add(-2 * time.Minute), Value: 940},
		{Timestamp: faultTime.Add(30 * time.Second), Value: 910},
		{Timestamp: faultTime.Add(time.Minute), Value: 930},
	}

	analysis := analyzeMemoryPressure(points, 1000, true, faultTime)
	if !analysis.LimitTooLowPattern || analysis.ApplicationLeakPattern {
		t.Fatalf("expected limit-too-low shape, got %+v", analysis)
	}
	if analysis.MemoryPattern != memoryPatternLimitTooLow {
		t.Fatalf("unexpected pattern %q", analysis.MemoryPattern)
	}
}

func TestAnalyzeMemoryPressureClassifiesLeakShape(t *testing.T) {
	faultTime := time.Unix(1_000, 0)
	points := []prometheus.Point{
		{Timestamp: faultTime.Add(-5 * time.Minute), Value: 300},
		{Timestamp: faultTime.Add(-4 * time.Minute), Value: 500},
		{Timestamp: faultTime.Add(-2 * time.Minute), Value: 900},
		{Timestamp: faultTime.Add(-30 * time.Second), Value: 980},
		{Timestamp: faultTime.Add(30 * time.Second), Value: 220},
		{Timestamp: faultTime.Add(2 * time.Minute), Value: 460},
		{Timestamp: faultTime.Add(4 * time.Minute), Value: 720},
	}

	analysis := analyzeMemoryPressure(points, 1000, true, faultTime)
	if !analysis.ApplicationLeakPattern || analysis.LimitTooLowPattern {
		t.Fatalf("expected application leak shape, got %+v", analysis)
	}
	if analysis.MemoryPattern != memoryPatternApplicationLeak {
		t.Fatalf("unexpected pattern %q", analysis.MemoryPattern)
	}
	if analysis.PreOOMSlopeRatioPerMinute <= 0 {
		t.Fatalf("expected positive pre-OOM slope, got %+v", analysis)
	}
}

func TestOOMKilledAnalyzerDoesNotMatchProbeKillExit137WithoutOOMSignal(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "probe-kill"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app",
				LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						Reason:   "Error",
						ExitCode: 137,
					},
				},
			}},
		},
	}
	ctx := &diagnostic.DiagnosticContext{
		Pod: pod,
		Events: []corev1.Event{{
			Reason:  "Unhealthy",
			Message: "Liveness probe failed: connection refused",
		}},
	}

	if NewOOMKilledAnalyzer().Match(ctx) {
		t.Fatal("expected liveness-probe SIGKILL without OOM signal not to match OOMKilled")
	}
}

func TestOOMKilledAnalyzerMatchesExit137WithOOMLogSignal(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "oom-log"},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app",
				LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						Reason:   "Error",
						ExitCode: 137,
					},
				},
			}},
		},
	}
	ctx := &diagnostic.DiagnosticContext{
		Pod: pod,
		Logs: []diagnostic.ContainerLogs{{
			ContainerName: "app",
			Previous:      "fatal: out of memory while allocating buffer",
		}},
	}

	if !NewOOMKilledAnalyzer().Match(ctx) {
		t.Fatal("expected exit 137 with OOM log evidence to match OOMKilled")
	}
}
