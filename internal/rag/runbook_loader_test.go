package rag

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRunbooksFrontmatterPlannerHints(t *testing.T) {
	dir := t.TempDir()
	content := `---
fault_type: OOMKilled
checks:
  - inspect termination state
recommended_tools:
  - k8s.get_previous_logs
  - prometheus.query_range
remediation_candidates:
  - adjust_memory_limit
risk_policy:
  adjust_memory_limit: high
stop_conditions:
  - confirmed hypothesis
---
# OOMKilled

## checks
Look at memory usage.
`
	if err := os.WriteFile(filepath.Join(dir, "oom.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runbooks, err := LoadRunbooks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(runbooks) != 1 {
		t.Fatalf("expected one runbook, got %d", len(runbooks))
	}
	if runbooks[0].FaultType != "OOMKilled" {
		t.Fatalf("frontmatter fault type not applied: %s", runbooks[0].FaultType)
	}
	if len(runbooks[0].Hints.RecommendedTools) != 2 {
		t.Fatalf("expected recommended tools, got %#v", runbooks[0].Hints.RecommendedTools)
	}
	if runbooks[0].Sections[0].Title != "planner_hints" {
		t.Fatalf("expected planner hints section, got %s", runbooks[0].Sections[0].Title)
	}
}
