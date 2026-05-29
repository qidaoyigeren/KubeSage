package analyzer

import (
	"strings"
	"testing"
	"time"

	"kubesage/internal/diagnostic"
)

// TestKeyLogEvidences verifies keyword-matching logs are emitted as separate
// k8s_key_log evidence.
func TestKeyLogEvidences(t *testing.T) {
	faultTime := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	logs := []diagnostic.ContainerLogs{
		{
			ContainerName: "app",
			Previous:      "normal line\npanic: config missing\nanother line",
			FaultTime:     faultTime,
			WindowStart:   faultTime.Add(-2 * time.Minute),
			WindowEnd:     faultTime.Add(time.Minute),
			Precise:       true,
		},
	}

	evidences := keyLogEvidences(logs, "Key CrashLoopBackOff log fragments", crashLoopLogKeywords, "warning")
	if len(evidences) != 1 {
		t.Fatalf("expected one evidence, got %d", len(evidences))
	}
	if evidences[0].SourceType != "k8s_key_log" {
		t.Fatalf("unexpected source type: %s", evidences[0].SourceType)
	}
	if !strings.Contains(evidences[0].Content, "panic: config missing") {
		t.Fatalf("missing matched log line: %s", evidences[0].Content)
	}
}
