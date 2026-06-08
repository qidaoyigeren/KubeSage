package main

import (
	"fmt"
	"testing"

	evalpkg "kubesage/internal/eval"
)

func TestBuildScheduleBalancesExactSampleCount(t *testing.T) {
	cases := make([]evalpkg.EvalCase, 59)
	for i := range cases {
		cases[i].ID = fmt.Sprintf("case-%02d", i+1)
	}

	schedule := buildSchedule(cases, 150)
	if len(schedule) != 150 {
		t.Fatalf("schedule length = %d, want 150", len(schedule))
	}

	counts := map[string]int{}
	sampleIDs := map[string]bool{}
	for _, item := range schedule {
		counts[item.Case.ID]++
		if sampleIDs[item.SampleID] {
			t.Fatalf("duplicate sample ID %q", item.SampleID)
		}
		sampleIDs[item.SampleID] = true
	}

	twoRuns := 0
	threeRuns := 0
	for _, count := range counts {
		switch count {
		case 2:
			twoRuns++
		case 3:
			threeRuns++
		default:
			t.Fatalf("scenario run count = %d, want 2 or 3", count)
		}
	}
	if twoRuns != 27 || threeRuns != 32 {
		t.Fatalf("distribution = %d scenarios x2 and %d scenarios x3, want 27 and 32", twoRuns, threeRuns)
	}
}

func TestLoadCasePathsCombinesLiveSuites(t *testing.T) {
	cases, err := loadCasePaths("../../eval/cases/core.yaml,../../eval/cases/fault-bank.yaml,../../eval/cases/supplementary.yaml")
	if err != nil {
		t.Fatalf("loadCasePaths() error = %v", err)
	}
	if len(cases) != 59 {
		t.Fatalf("loaded cases = %d, want 59", len(cases))
	}
}

func TestRepeatConsistency(t *testing.T) {
	cases := []caseResult{
		{ScenarioID: "stable", Arms: map[string]armResult{armAgent: {ActualFaultType: "OOMKilled"}}},
		{ScenarioID: "stable", Arms: map[string]armResult{armAgent: {ActualFaultType: "OOMKilled"}}},
		{ScenarioID: "unstable", Arms: map[string]armResult{armAgent: {ActualFaultType: "PodPending"}}},
		{ScenarioID: "unstable", Arms: map[string]armResult{armAgent: {ActualFaultType: "unknown"}}},
	}
	if got := repeatConsistency(armAgent, cases); got != 0.5 {
		t.Fatalf("repeat consistency = %v, want 0.5", got)
	}
}
