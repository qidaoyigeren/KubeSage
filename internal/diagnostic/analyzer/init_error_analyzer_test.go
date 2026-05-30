package analyzer

import (
	"testing"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestInitErrorAnalyzer_Match(t *testing.T) {
	tests := []struct {
		name string
		ctx  *diagnostic.DiagnosticContext
		want bool
	}{
		{
			name: "nil pod",
			ctx:  &diagnostic.DiagnosticContext{},
			want: false,
		},
		{
			name: "init container terminated with error",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								State: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "init container waiting with reason",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								State: corev1.ContainerState{
									Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "all init containers completed",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						InitContainerStatuses: []corev1.ContainerStatus{
							{
								State: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{ExitCode: 0},
								},
							},
						},
					},
				},
			},
			want: false,
		},
	}
	a := NewInitErrorAnalyzer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.Match(tt.ctx); got != tt.want {
				t.Errorf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInitErrorAnalyzer_Analyze(t *testing.T) {
	a := NewInitErrorAnalyzer()
	ctx := &diagnostic.DiagnosticContext{
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				InitContainers: []corev1.Container{
					{Name: "init-db", Image: "busybox", Command: []string{"sh", "-c", "until nc -z db 5432; do sleep 1; done"}},
				},
			},
			Status: corev1.PodStatus{
				InitContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "init-db",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
								Reason:   "Error",
							},
						},
					},
				},
			},
		},
		Events: []corev1.Event{
			{
				Reason:        "Failed",
				Message:       "Error: init container init-db failed",
				Type:          "Warning",
				LastTimestamp:  metav1.Now(),
			},
		},
	}
	result, err := a.Analyze(ctx)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if result.FaultType != "InitError" {
		t.Errorf("FaultType = %v, want InitError", result.FaultType)
	}
	if result.ConfidenceScore < 0.6 {
		t.Errorf("ConfidenceScore = %v, want >= 0.6", result.ConfidenceScore)
	}
}
