package alertmanager

import (
	"testing"
	"time"
)

// TestParseAlertFromLabels verifies standard Alertmanager labels are mapped to
// a diagnosis request shape.
func TestParseAlertFromLabels(t *testing.T) {
	start := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	parsed := parseAlert(WebhookPayload{}, Alert{
		Status:   "firing",
		StartsAt: start,
		Labels: map[string]string{
			"namespace": "default",
			"pod":       "api-123",
			"container": "api",
			"alertname": "KubePodCrashLooping",
			"severity":  "warning",
		},
	})

	if parsed.Namespace != "default" || parsed.PodName != "api-123" || parsed.ContainerName != "api" {
		t.Fatalf("unexpected kubernetes labels: %#v", parsed)
	}
	if parsed.FaultType != "CrashLoopBackOff" {
		t.Fatalf("unexpected fault type: %s", parsed.FaultType)
	}
	if parsed.AlertTime == nil || !parsed.AlertTime.Equal(start) {
		t.Fatalf("unexpected alert time: %v", parsed.AlertTime)
	}
}

// TestParseAlertPodFromDescription verifies pod fallback parsing when the pod
// label is absent.
func TestParseAlertPodFromDescription(t *testing.T) {
	parsed := parseAlert(WebhookPayload{
		CommonLabels: map[string]string{
			"namespace": "prod",
			"alertname": "KubePodOOMKilled",
		},
	}, Alert{
		Annotations: map[string]string{
			"description": "container restarted in pod api-7c9c9d6b5d-abcde due to OOMKilled",
		},
	})

	if parsed.PodName != "api-7c9c9d6b5d-abcde" {
		t.Fatalf("expected pod from description, got %s", parsed.PodName)
	}
	if parsed.FaultType != "OOMKilled" {
		t.Fatalf("unexpected fault type: %s", parsed.FaultType)
	}
}

// TestParsePodNameFromDescriptionNamespaceForm verifies namespace/pod text
// returns only the pod segment.
func TestParsePodNameFromDescriptionNamespaceForm(t *testing.T) {
	got := parsePodNameFromDescription("Pod prod/api-7c9c9d6b5d-abcde has been unhealthy")
	if got != "api-7c9c9d6b5d-abcde" {
		t.Fatalf("expected pod segment, got %s", got)
	}
}

// TestFaultTypeFromAlertName verifies the supported alertname mapping table.
func TestFaultTypeFromAlertName(t *testing.T) {
	cases := map[string]string{
		"KubePodCrashLooping": "CrashLoopBackOff",
		"KubePodOOMKilled":    "OOMKilled",
		"KubePodNotReady":     "ProbeFailed/Pending",
		"KubePodPending":      "Pending",
	}
	for alertName, expected := range cases {
		if got := faultTypeFromAlertName(alertName); got != expected {
			t.Fatalf("alert %s expected %s, got %s", alertName, expected, got)
		}
	}
}
