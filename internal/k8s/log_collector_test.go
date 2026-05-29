package k8s

import (
	"strings"
	"testing"
	"time"
)

// TestFilterTimestampedLogWindow verifies that log lines outside the end of the
// precise window are removed after a SinceTime query.
func TestFilterTimestampedLogWindow(t *testing.T) {
	start := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	end := start.Add(1 * time.Minute)
	text := strings.Join([]string{
		start.Add(-time.Second).Format(time.RFC3339Nano) + " before window",
		start.Add(10*time.Second).Format(time.RFC3339Nano) + " inside panic",
		end.Add(time.Second).Format(time.RFC3339Nano) + " after window",
		"untimestamped context",
	}, "\n")

	got := filterTimestampedLogWindow(text, start, end)
	if strings.Contains(got, "before window") {
		t.Fatalf("expected before-window line to be filtered: %s", got)
	}
	if strings.Contains(got, "after window") {
		t.Fatalf("expected after-window line to be filtered: %s", got)
	}
	if !strings.Contains(got, "inside panic") || !strings.Contains(got, "untimestamped context") {
		t.Fatalf("expected in-window and context lines: %s", got)
	}
}

// TestLogWindowFallback verifies that missing fault time falls back to recent
// tail-based log collection.
func TestLogWindowFallback(t *testing.T) {
	_, _, precise, fallback := logWindow(LogCollectionOptions{})
	if precise {
		t.Fatal("expected fallback mode without fault time")
	}
	if fallback == "" {
		t.Fatal("expected fallback reason")
	}
}
