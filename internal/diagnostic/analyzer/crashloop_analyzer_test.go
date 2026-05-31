package analyzer

import (
	"testing"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCrashLoopAnalyzer_Match(t *testing.T) {
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
			name: "CrashLoopBackOff waiting state",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
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
			name: "restart count with terminated",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								RestartCount: 3,
								LastTerminationState: corev1.ContainerState{
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
			name: "running container no restarts",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{RestartCount: 0},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "OOMKilled termination belongs to OOM analyzer",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								RestartCount: 2,
								LastTerminationState: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137},
								},
							},
						},
					},
				},
			},
			want: false,
		},
	}
	a := NewCrashLoopBackOffAnalyzer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.Match(tt.ctx); got != tt.want {
				t.Errorf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCrashLoopAnalyzer_Analyze(t *testing.T) {
	a := NewCrashLoopBackOffAnalyzer()
	ctx := &diagnostic.DiagnosticContext{
		Pod: &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:         "app",
						RestartCount: 5,
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
						},
						LastTerminationState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode:   1,
								FinishedAt: metav1.Now(),
							},
						},
					},
				},
			},
		},
		Events: []corev1.Event{
			{
				Reason:        "BackOff",
				Message:       "Back-off restarting failed container",
				Type:          "Warning",
				LastTimestamp: metav1.Now(),
			},
		},
	}
	result, err := a.Analyze(ctx)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if result.FaultType != "CrashLoopBackOff" {
		t.Errorf("FaultType = %v, want CrashLoopBackOff", result.FaultType)
	}
	if result.ConfidenceScore < 0.7 {
		t.Errorf("ConfidenceScore = %v, want >= 0.7", result.ConfidenceScore)
	}
	if len(result.Evidences) == 0 {
		t.Error("Evidences should not be empty")
	}
}
