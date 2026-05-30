package analyzer

import (
	"strings"

	"kubesage/internal/diagnostic"
)

// clampConfidence keeps analyzer confidence scores in a stable [0,1] range.
func clampConfidence(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}

// hasEvidence reports whether any evidence matches the given source/title hint.
func hasEvidence(records []diagnostic.EvidenceRecord, sourceType, titleContains string) bool {
	for _, record := range records {
		if sourceType != "" && record.SourceType != sourceType {
			continue
		}
		if titleContains != "" && !strings.Contains(strings.ToLower(record.Title), strings.ToLower(titleContains)) {
			continue
		}
		return true
	}
	return false
}
