package analyzer

import (
	"testing"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestImagePullAnalyzer_Match(t *testing.T) {
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
			name: "ImagePullBackOff waiting state",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								State: corev1.ContainerState{
									Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "ErrImagePull waiting state",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								State: corev1.ContainerState{
									Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"},
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "failed pull event",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{}},
				},
				Events: []corev1.Event{
					{Message: "Failed to pull image \"nginx:latest\": not found"},
				},
			},
			want: true,
		},
		{
			name: "normal pulled event is not image pull failure",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{}},
				},
				Events: []corev1.Event{
					{Reason: "Pulled", Message: "Container image \"busybox\" already present on machine and can be accessed by the pod"},
				},
			},
			want: false,
		},
		{
			name: "running container no pull issues",
			ctx: &diagnostic.DiagnosticContext{
				Pod: &corev1.Pod{
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
						},
					},
				},
			},
			want: false,
		},
	}
	a := NewImagePullBackOffAnalyzer()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.Match(tt.ctx); got != tt.want {
				t.Errorf("Match() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestImagePullAnalyzer_Analyze(t *testing.T) {
	a := NewImagePullBackOffAnalyzer()
	ctx := &diagnostic.DiagnosticContext{
		Pod: &corev1.Pod{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: "nginx:latest"},
				},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "app",
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"},
						},
					},
				},
			},
		},
		Events: []corev1.Event{
			{
				Reason:        "Failed",
				Message:       "Failed to pull image \"nginx:latest\": rpc error",
				Type:          "Warning",
				LastTimestamp: metav1.Now(),
			},
		},
	}
	result, err := a.Analyze(ctx)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if result.FaultType != "ImagePullBackOff" {
		t.Errorf("FaultType = %v, want ImagePullBackOff", result.FaultType)
	}
	if result.ConfidenceScore < 0.6 {
		t.Errorf("ConfidenceScore = %v, want >= 0.6", result.ConfidenceScore)
	}
}
