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

// hasQoSEvidence checks if evidence contains a QoS class indicator matching
// the given class name (Guaranteed, Burstable, BestEffort). Per K8s docs,
// QoS class affects OOM kill priority and eviction order.
func hasQoSEvidence(records []diagnostic.EvidenceRecord, qosClass string) bool {
	lower := strings.ToLower(qosClass)
	for _, record := range records {
		text := strings.ToLower(record.Content)
		if strings.Contains(text, "qos="+lower) || strings.Contains(text, "qosclass="+lower) ||
			strings.Contains(text, "qosclass: "+lower) {
			return true
		}
	}
	return false
}

// hasHighRestartCount checks if any evidence indicates a restart count above
// the given threshold. High restart counts confirm recurring failure patterns
// rather than transient issues.
func hasHighRestartCount(records []diagnostic.EvidenceRecord, threshold int) bool {
	for _, record := range records {
		if record.SourceType != "k8s_pod_status" {
			continue
		}
		text := strings.ToLower(record.Content)
		// Parse "restartCount=N" pattern from evidence content.
		if idx := strings.Index(text, "restartcount="); idx >= 0 {
			numStr := text[idx+len("restartcount="):]
			end := 0
			for end < len(numStr) && numStr[end] >= '0' && numStr[end] <= '9' {
				end++
			}
			if end > 0 {
				count := 0
				for _, ch := range numStr[:end] {
					count = count*10 + int(ch-'0')
				}
				if count > threshold {
					return true
				}
			}
		}
	}
	return false
}

// hasEvidenceContent checks if any evidence's Content contains the given text
// (case-insensitive). Useful for detecting specific patterns in evidence.
func hasEvidenceContent(records []diagnostic.EvidenceRecord, contentContains string) bool {
	lower := strings.ToLower(contentContains)
	for _, record := range records {
		if strings.Contains(strings.ToLower(record.Content), lower) {
			return true
		}
	}
	return false
}

// evidenceContentMatches checks if any evidence matches sourceType and has
// content containing the given text (case-insensitive).
func evidenceContentMatches(records []diagnostic.EvidenceRecord, sourceType, contentContains string) bool {
	lower := strings.ToLower(contentContains)
	for _, record := range records {
		if record.SourceType != sourceType {
			continue
		}
		if strings.Contains(strings.ToLower(record.Content), lower) {
			return true
		}
	}
	return false
}
