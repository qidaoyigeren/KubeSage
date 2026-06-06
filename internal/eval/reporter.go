package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteJSON writes the full suite result artifact.
func WriteJSON(path string, result *EvalSuiteResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal eval result: %w", err)
	}
	return writeFile(path, append(data, '\n'))
}

// ReadJSON loads a suite result artifact.
func ReadJSON(path string) (*EvalSuiteResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read eval result %s: %w", path, err)
	}
	var result EvalSuiteResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse eval result %s: %w", path, err)
	}
	return &result, nil
}

// WriteMarkdown writes a concise human-readable RCA eval report.
func WriteMarkdown(path string, result *EvalSuiteResult) error {
	return writeFile(path, []byte(MarkdownReport(result)))
}

// MarkdownReport renders an eval suite as Markdown.
func MarkdownReport(result *EvalSuiteResult) string {
	if result == nil {
		return "# RCA Eval Results\n\nNo result available.\n"
	}
	var b strings.Builder
	m := result.Metrics
	b.WriteString("# RCA Eval Results\n\n")
	b.WriteString(fmt.Sprintf("- Generated: %s\n", result.GeneratedAt.Format("2006-01-02 15:04:05 UTC")))
	if result.CasesPath != "" {
		b.WriteString(fmt.Sprintf("- Cases: `%s`\n", result.CasesPath))
	}
	b.WriteString(fmt.Sprintf("- Total: %d\n", m.TotalCases))
	b.WriteString(fmt.Sprintf("- Passed: %d\n", m.PassedCases))
	b.WriteString(fmt.Sprintf("- RCA accuracy: %.1f%%\n", m.RCAAccuracy*100))
	b.WriteString(fmt.Sprintf("- Fault type accuracy: %.1f%%\n", m.FaultTypeAccuracy*100))
	b.WriteString(fmt.Sprintf("- Root cause accuracy: %.1f%%\n", m.RootCauseAccuracy*100))
	b.WriteString(fmt.Sprintf("- Key evidence recall: %.1f%%\n", m.KeyEvidenceRecall*100))
	b.WriteString(fmt.Sprintf("- Remediation recall: %.1f%%\n", m.RemediationRecall*100))
	b.WriteString(fmt.Sprintf("- Hallucination rate: %.1f%%\n", m.HallucinationRate*100))
	b.WriteString(fmt.Sprintf("- Overconfidence rate: %.1f%%\n", m.OverconfidenceRate*100))
	b.WriteString(fmt.Sprintf("- Safety pass rate: %.1f%%\n", m.SafetyPassRate*100))
	b.WriteString(fmt.Sprintf("- Dangerous suggestions: %d\n", m.DangerousSuggestionCount))
	b.WriteString(fmt.Sprintf("- Average duration: %d ms\n", m.AverageDurationMS))
	b.WriteString(fmt.Sprintf("- P95 duration: %d ms\n\n", m.P95DurationMS))
	b.WriteString("| Case | Expected | Actual | Confidence | Duration | Result |\n")
	b.WriteString("| --- | --- | --- | ---: | ---: | --- |\n")
	for _, r := range result.Results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | %.2f | %d ms | %s |\n",
			r.ID,
			r.ExpectedFaultType,
			r.ActualFaultType,
			r.Confidence,
			r.DurationMS,
			status,
		))
	}
	failures := failedResults(result.Results)
	if len(failures) > 0 {
		b.WriteString("\n## Failures\n\n")
		for _, r := range failures {
			b.WriteString(fmt.Sprintf("### %s\n\n", r.ID))
			b.WriteString(fmt.Sprintf("- Expected: `%s`, actual: `%s`\n", r.ExpectedFaultType, r.ActualFaultType))
			b.WriteString(fmt.Sprintf("- Fault type matched: %t\n", r.FaultTypeMatched))
			b.WriteString(fmt.Sprintf("- Root cause matched: %t\n", r.RootCauseMatched))
			if len(r.MissingEvidence) > 0 {
				b.WriteString(fmt.Sprintf("- Missing evidence: %s\n", strings.Join(r.MissingEvidence, "; ")))
			}
			if len(r.MissingRemediations) > 0 {
				b.WriteString(fmt.Sprintf("- Missing remediation: %s\n", strings.Join(r.MissingRemediations, "; ")))
			}
			if len(r.UnexpectedTerms) > 0 {
				b.WriteString(fmt.Sprintf("- Unexpected terms: %s\n", strings.Join(r.UnexpectedTerms, "; ")))
			}
			if len(r.Errors) > 0 {
				b.WriteString(fmt.Sprintf("- Errors: %s\n", strings.Join(r.Errors, "; ")))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// WriteComparisonReport writes a before/after Markdown comparison table.
func WriteComparisonReport(path string, before, after *EvalSuiteResult) error {
	return writeFile(path, []byte(ComparisonReport(before, after)))
}

// ComparisonReport renders a before/after suite diff as Markdown.
func ComparisonReport(before, after *EvalSuiteResult) string {
	var b strings.Builder
	b.WriteString("# RCA Eval Comparison\n\n")
	b.WriteString("| Metric | Before | After | Delta |\n")
	b.WriteString("| --- | ---: | ---: | ---: |\n")
	writeMetric := func(name string, beforeValue, afterValue float64, percent bool) {
		delta := afterValue - beforeValue
		if percent {
			b.WriteString(fmt.Sprintf("| %s | %.1f%% | %.1f%% | %+0.1f%% |\n", name, beforeValue*100, afterValue*100, delta*100))
			return
		}
		b.WriteString(fmt.Sprintf("| %s | %.0f | %.0f | %+0.0f |\n", name, beforeValue, afterValue, delta))
	}
	var bm, am EvalMetrics
	if before != nil {
		bm = before.Metrics
	}
	if after != nil {
		am = after.Metrics
	}
	writeMetric("Total cases", float64(bm.TotalCases), float64(am.TotalCases), false)
	writeMetric("Passed cases", float64(bm.PassedCases), float64(am.PassedCases), false)
	writeMetric("Failed cases", float64(bm.FailedCases), float64(am.FailedCases), false)
	writeMetric("RCA accuracy", bm.RCAAccuracy, am.RCAAccuracy, true)
	writeMetric("Fault type accuracy", bm.FaultTypeAccuracy, am.FaultTypeAccuracy, true)
	writeMetric("Root cause accuracy", bm.RootCauseAccuracy, am.RootCauseAccuracy, true)
	writeMetric("Key evidence recall", bm.KeyEvidenceRecall, am.KeyEvidenceRecall, true)
	writeMetric("Remediation recall", bm.RemediationRecall, am.RemediationRecall, true)
	writeMetric("Hallucination rate", bm.HallucinationRate, am.HallucinationRate, true)
	writeMetric("Overconfidence rate", bm.OverconfidenceRate, am.OverconfidenceRate, true)
	writeMetric("Safety pass rate", bm.SafetyPassRate, am.SafetyPassRate, true)
	writeMetric("Dangerous suggestions", float64(bm.DangerousSuggestionCount), float64(am.DangerousSuggestionCount), false)
	writeMetric("Average duration ms", float64(bm.AverageDurationMS), float64(am.AverageDurationMS), false)
	writeMetric("P95 duration ms", float64(bm.P95DurationMS), float64(am.P95DurationMS), false)
	b.WriteString("\n## Case Changes\n\n")
	b.WriteString("| Case | Before | After |\n")
	b.WriteString("| --- | --- | --- |\n")
	beforeByID := map[string]EvalCaseResult{}
	if before != nil {
		for _, r := range before.Results {
			beforeByID[r.ID] = r
		}
	}
	seen := map[string]bool{}
	if after != nil {
		for _, r := range after.Results {
			seen[r.ID] = true
			b.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", r.ID, passLabel(beforeByID[r.ID].Passed), passLabel(r.Passed)))
		}
	}
	if before != nil {
		for _, r := range before.Results {
			if seen[r.ID] {
				continue
			}
			b.WriteString(fmt.Sprintf("| `%s` | %s | missing |\n", r.ID, passLabel(r.Passed)))
		}
	}
	return b.String()
}

func failedResults(results []EvalCaseResult) []EvalCaseResult {
	failures := []EvalCaseResult{}
	for _, r := range results {
		if !r.Passed {
			failures = append(failures, r)
		}
	}
	return failures
}

func passLabel(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}

func writeFile(path string, data []byte) error {
	if path == "" || path == "-" {
		_, err := os.Stdout.Write(data)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
