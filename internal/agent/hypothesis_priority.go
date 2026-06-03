package agent

import (
	"math"
	"strings"

	"kubesage/internal/model"
)

const primaryConfidenceTieEpsilon = 0.03

func betterPrimaryHypothesis(candidate, current model.Hypothesis) bool {
	candidateStatus := primaryStatusRank(candidate.Status)
	currentStatus := primaryStatusRank(current.Status)
	if candidateStatus != currentStatus {
		return candidateStatus > currentStatus
	}
	if diff := candidate.ConfidenceScore - current.ConfidenceScore; math.Abs(diff) > primaryConfidenceTieEpsilon {
		return diff > 0
	}
	candidateSpecificity := hypothesisSpecificityRank(candidate.HypothesisType)
	currentSpecificity := hypothesisSpecificityRank(current.HypothesisType)
	if candidateSpecificity != currentSpecificity {
		return candidateSpecificity > currentSpecificity
	}
	candidateRefs := len(jsonTextItems(candidate.SupportingEvidenceRefs))
	currentRefs := len(jsonTextItems(current.SupportingEvidenceRefs))
	if candidateRefs != currentRefs {
		return candidateRefs > currentRefs
	}
	return strings.Compare(candidate.HypothesisType, current.HypothesisType) < 0
}

func primaryStatusRank(status string) int {
	switch status {
	case model.HypothesisStatusConfirmed:
		return 3
	case model.HypothesisStatusActive:
		return 2
	case "":
		return 1
	default:
		return 0
	}
}

func hypothesisSpecificityRank(hypothesisType string) int {
	switch strings.ToLower(strings.TrimSpace(hypothesisType)) {
	case "pvc_unbound":
		return 100
	case "missing_secret_or_configmap":
		return 98
	case "memory_limit_too_low":
		return 96
	case "node_not_ready":
		return 94
	case "probe_misconfigured", "image_pull_failed", "init_container_crash", "node_eviction":
		return 90
	case "dependency_unavailable":
		return 82
	case "application_memory_leak":
		return 80
	case "bad_config":
		return 72
	case "scheduling_constraint":
		return 60
	default:
		return 50
	}
}
