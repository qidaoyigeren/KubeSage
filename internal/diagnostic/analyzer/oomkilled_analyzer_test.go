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
		}
	}
	if !found {
		t.Fatal("expected prometheus memory evidence")
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
