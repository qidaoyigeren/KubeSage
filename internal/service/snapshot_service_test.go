package service

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestResolveFaultTimePrefersTermination verifies the primary fault time source
// is lastState.terminated.finishedAt.
func TestResolveFaultTimePrefersTermination(t *testing.T) {
	terminationTime := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	eventTime := terminationTime.Add(5 * time.Minute)
	alertTime := terminationTime.Add(10 * time.Minute)
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "app",
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							FinishedAt: metav1.NewTime(terminationTime),
						},
					},
				},
			},
		},
	}
	events := []corev1.Event{{Reason: "Unhealthy", LastTimestamp: metav1.NewTime(eventTime)}}

	got, fallback := resolveFaultTime(pod, events, &alertTime)
	if !got.Equal(terminationTime) {
		t.Fatalf("expected termination time, got %s", got)
	}
	if fallback != "" {
		t.Fatalf("unexpected fallback: %s", fallback)
	}
}

// TestResolveFaultTimeFallsBackToEvent verifies event timestamps are used when
// pod termination time is unavailable.
func TestResolveFaultTimeFallsBackToEvent(t *testing.T) {
	eventTime := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	events := []corev1.Event{{Reason: "Unhealthy", LastTimestamp: metav1.NewTime(eventTime)}}

	got, fallback := resolveFaultTime(&corev1.Pod{}, events, nil)
	if !got.Equal(eventTime) {
		t.Fatalf("expected event time, got %s", got)
	}
	if fallback != "" {
		t.Fatalf("unexpected fallback: %s", fallback)
	}
}

// TestResolveFaultTimeFallsBackToAlert verifies alert time is used when pod and
// event timestamps are unavailable.
func TestResolveFaultTimeFallsBackToAlert(t *testing.T) {
	alertTime := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	got, fallback := resolveFaultTime(&corev1.Pod{}, nil, &alertTime)
	if !got.Equal(alertTime) {
		t.Fatalf("expected alert time, got %s", got)
	}
	if fallback != "" {
		t.Fatalf("unexpected fallback: %s", fallback)
	}
}
