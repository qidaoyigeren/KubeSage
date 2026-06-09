package agent

import (
	"fmt"
	"strings"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

// IntegrityReport summarizes the hypothesis-evidence coverage analysis.
type IntegrityReport struct {
	IsComplete          bool                 `json:"is_complete"`
	Gaps                []IntegrityGap       `json:"gaps"`
	CoverageRate        float64              `json:"coverage_rate"`
	HypothesisBreakdown []HypothesisCoverage `json:"hypothesis_breakdown"`
	Summary             string               `json:"summary"`
}

// IntegrityGap describes a gap in the evidence chain for a hypothesis.
type IntegrityGap struct {
	HypothesisType string `json:"hypothesis_type"`
	Severity       string `json:"severity"` // "critical", "warning", "info"
	Description    string `json:"description"`
	Recommendation string `json:"recommendation,omitempty"`
}

// HypothesisCoverage tracks evidence coverage for a single hypothesis.
type HypothesisCoverage struct {
	Type                string   `json:"type"`
	Confidence          float64  `json:"confidence"`
	Status              string   `json:"status"`
	SupportingCount     int      `json:"supporting_count"`
	ContradictingCount  int      `json:"contradicting_count"`
	MissingEvidenceDesc []string `json:"missing_evidence_desc,omitempty"`
	IsWellSupported     bool     `json:"is_well_supported"`
}

const (
	minSupportingEvidencePerHypothesis = 2
	minConfidenceForCoverageCheck      = 0.3
)

// CheckHypothesisCoverage verifies that each active hypothesis has adequate
// evidence support and that no critical gaps exist in the evidence chain.
// Returns a report with coverage metrics and actionable recommendations.
func CheckHypothesisCoverage(
	hypotheses []HypothesisScore,
	evidence []diagnostic.EvidenceRecord,
) IntegrityReport {
	report := IntegrityReport{
		IsComplete:          true,
		HypothesisBreakdown: make([]HypothesisCoverage, 0, len(hypotheses)),
	}

	// Build evidence lookup by ref.
	evidenceByRef := make(map[string]diagnostic.EvidenceRecord, len(evidence))
	for i, ev := range evidence {
		ref := evidenceRef(ev, i)
		evidenceByRef[ref] = ev
	}

	totalSupported := 0
	totalChecked := 0

	for _, h := range hypotheses {
		// Skip low-confidence or rejected hypotheses.
		if h.Confidence < minConfidenceForCoverageCheck ||
			h.Status == model.HypothesisStatusRejected {
			continue
		}
		totalChecked++

		coverage := HypothesisCoverage{
			Type:                h.Type,
			Confidence:          h.Confidence,
			Status:              h.Status,
			SupportingCount:     len(h.SupportingRefs),
			ContradictingCount:  len(h.ContradictingRefs),
			MissingEvidenceDesc: h.MissingEvidence,
		}

		// Check 1: Verify supporting evidence refs are valid.
		validSupporting := 0
		for _, ref := range h.SupportingRefs {
			if _, exists := evidenceByRef[ref]; exists {
				validSupporting++
			}
		}
		coverage.SupportingCount = validSupporting

		// Check 2: Minimum evidence requirement.
		if validSupporting < minSupportingEvidencePerHypothesis && h.Confidence > 0.5 {
			gap := IntegrityGap{
				HypothesisType: h.Type,
				Severity:       "warning",
				Description: fmt.Sprintf(
					"Hypothesis %q has only %d supporting evidence refs (minimum %d recommended)",
					h.Type, validSupporting, minSupportingEvidencePerHypothesis,
				),
				Recommendation: fmt.Sprintf(
					"Collect more evidence for %q before confirming", h.Type,
				),
			}
			report.Gaps = append(report.Gaps, gap)
			report.IsComplete = false
		}

		// Check 3: Contradicting evidence exists.
		if len(h.ContradictingRefs) > 0 && h.Status == model.HypothesisStatusConfirmed {
			gap := IntegrityGap{
				HypothesisType: h.Type,
				Severity:       "critical",
				Description: fmt.Sprintf(
					"Hypothesis %q is confirmed but has %d contradicting evidence refs",
					h.Type, len(h.ContradictingRefs),
				),
				Recommendation: "Review contradicting evidence before finalizing diagnosis",
			}
			report.Gaps = append(report.Gaps, gap)
			report.IsComplete = false
		}

		// Check 4: Missing evidence for active hypotheses.
		if len(h.MissingEvidence) > 0 && h.Status == model.HypothesisStatusActive {
			gap := IntegrityGap{
				HypothesisType: h.Type,
				Severity:       "info",
				Description: fmt.Sprintf(
					"Active hypothesis %q has %d missing evidence items: %s",
					h.Type, len(h.MissingEvidence),
					strings.Join(h.MissingEvidence[:minInt(3, len(h.MissingEvidence))], ", "),
				),
				Recommendation: fmt.Sprintf("Collect missing evidence for %q to confirm or reject", h.Type),
			}
			report.Gaps = append(report.Gaps, gap)
		}

		// Determine if well-supported.
		coverage.IsWellSupported = validSupporting >= minSupportingEvidencePerHypothesis &&
			coverage.ContradictingCount == 0

		if coverage.IsWellSupported {
			totalSupported++
		}
		report.HypothesisBreakdown = append(report.HypothesisBreakdown, coverage)
	}

	// Compute coverage rate.
	if totalChecked > 0 {
		report.CoverageRate = float64(totalSupported) / float64(totalChecked)
	} else {
		report.CoverageRate = 1.0 // no active hypotheses to check
		report.IsComplete = true
	}

	// Generate summary.
	if report.IsComplete && len(report.Gaps) == 0 {
		report.Summary = fmt.Sprintf(
			"All %d active hypotheses have adequate evidence support (coverage: %.0f%%)",
			totalChecked, report.CoverageRate*100,
		)
	} else {
		var criticalGaps, warningGaps, infoGaps int
		for _, gap := range report.Gaps {
			switch gap.Severity {
			case "critical":
				criticalGaps++
			case "warning":
				warningGaps++
			case "info":
				infoGaps++
			}
		}
		report.Summary = fmt.Sprintf(
			"%d active hypotheses checked, %d gaps found (%d critical, %d warning, %d info). Coverage: %.0f%%",
			totalChecked, len(report.Gaps), criticalGaps, warningGaps, infoGaps,
			report.CoverageRate*100,
		)
	}

	return report
}

// IntegrityReportToEvidence converts an IntegrityReport into an EvidenceRecord
// suitable for inclusion in the diagnostic report. This makes integrity
// information visible in the final output without changing existing interfaces.
func IntegrityReportToEvidence(report IntegrityReport) diagnostic.EvidenceRecord {
	severity := "info"
	if !report.IsComplete {
		severity = "warning"
	}
	hasCritical := false
	for _, gap := range report.Gaps {
		if gap.Severity == "critical" {
			hasCritical = true
			break
		}
	}
	if hasCritical {
		severity = "critical"
	}

	content := report.Summary
	for _, gap := range report.Gaps {
		content += fmt.Sprintf("\n  [%s] %s: %s → %s",
			gap.Severity, gap.HypothesisType, gap.Description, gap.Recommendation)
	}

	return diagnostic.EvidenceRecord{
		SourceType: "agent",
		Title:      "Evidence Integrity Check",
		Content:    content,
		Severity:   severity,
	}
}
