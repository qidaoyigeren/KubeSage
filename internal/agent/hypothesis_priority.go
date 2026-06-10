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

// hypothesisSpecificityRank assigns a tie-breaking priority rank based on how
// specific and actionable each hypothesis type is. Higher rank = more specific
// = preferred when confidence scores are tied.
//
// Ranking rationale (per K8s docs):
//   - pvc_unbound: PVC status is definitive — either bound or not (100)
//   - missing_secret_or_configmap: object inspection is definitive (98)
//   - memory_limit_too_low: requires metrics + limit comparison (96)
//   - node_not_ready: node condition is definitive (95)
//   - image_pull_failed: event message is very specific (93)
//   - init_container_crash: init container status is specific (92)
//   - node_eviction: eviction reason is specific (91)
//   - probe_misconfigured: probe config + events are specific (90)
//   - scheduling_constraint: FailedScheduling message is specific (88)
//   - dependency_unavailable: requires log pattern matching (82)
//   - application_memory_leak: requires metrics trend analysis (80)
//   - bad_config: broad category, less specific (72)
func hypothesisSpecificityRank(hypothesisType string) int {
	switch strings.ToLower(strings.TrimSpace(hypothesisType)) {
	case "pvc_unbound":
		return 100
	case "missing_secret_or_configmap":
		return 98
	case "memory_limit_too_low":
		return 96
	case "node_not_ready":
		return 95
	case "image_pull_failed":
		return 93
	case "init_container_crash":
		return 92
	case "node_eviction":
		return 91
	case "probe_misconfigured":
		return 90
	case "scheduling_constraint":
		// Per K8s docs: FailedScheduling events contain specific resource/taint/selector
		// information — more specific than dependency or config issues.
		return 88
	case "dependency_unavailable":
		return 82
	case "application_memory_leak":
		return 80
	case "bad_config":
		return 72
	default:
		return 50
	}
}
