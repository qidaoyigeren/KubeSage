package agent

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	convergenceMinTopGap           = 0.15
	convergenceHighConfidenceFloor = 0.65
)

type ConvergenceDecision struct {
	Converged              bool             `json:"converged"`
	Reason                 string           `json:"reason"`
	Top                    *HypothesisScore `json:"top,omitempty"`
	RunnerUp               *HypothesisScore `json:"runner_up,omitempty"`
	Top1Top2Margin         float64          `json:"top1_top2_margin"`
	KeyEvidenceComplete    bool             `json:"key_evidence_complete"`
	Ambiguous              bool             `json:"ambiguous"`
	MultipleConfirmed      bool             `json:"multiple_confirmed"`
	ConfirmedTypes         []string         `json:"confirmed_types,omitempty"`
	DistinguishingEvidence []string         `json:"distinguishing_evidence,omitempty"`
	HighConfidenceTypes    []string         `json:"high_confidence_types,omitempty"`
}

func (e *HypothesisEngine) DecideConvergence(scores []HypothesisScore) ConvergenceDecision {
	threshold := convergenceMinConfidence
	if e != nil && e.config.ConfirmedThreshold > 0 {
		threshold = e.config.ConfirmedThreshold
	}
	return assessHypothesisConvergence(scores, threshold)
}

func assessHypothesisConvergence(scores []HypothesisScore, thresholds ...float64) ConvergenceDecision {
	threshold := convergenceMinConfidence
	if len(thresholds) > 0 && thresholds[0] > 0 {
		threshold = thresholds[0]
	}
	ranked := rankedHypotheses(scores)
	if len(ranked) == 0 {
		return ConvergenceDecision{Reason: "no hypotheses available"}
	}

	top := ranked[0]
	confirmed := confirmedTypes(ranked, threshold)
	decision := ConvergenceDecision{
		Top:                    &top,
		Top1Top2Margin:         top.Confidence,
		KeyEvidenceComplete:    keyEvidenceComplete(top),
		MultipleConfirmed:      len(confirmed) > 1,
		ConfirmedTypes:         confirmed,
		DistinguishingEvidence: distinguishingEvidence(ranked),
		HighConfidenceTypes:    highConfidenceTypes(ranked),
	}
	if len(ranked) > 1 {
		runnerUp := ranked[1]
		decision.RunnerUp = &runnerUp
		decision.Top1Top2Margin = top.Confidence - runnerUp.Confidence
	}

	switch {
	case top.Confidence < threshold:
		decision.Reason = fmt.Sprintf("top hypothesis confidence %.2f is below %.2f", top.Confidence, threshold)
	case !decision.KeyEvidenceComplete:
		decision.Reason = "top hypothesis still has missing key evidence"
	case decision.RunnerUp != nil &&
		decision.RunnerUp.Confidence >= convergenceHighConfidenceFloor &&
		decision.Top1Top2Margin < convergenceMinTopGap:
		decision.Ambiguous = true
		decision.Reason = fmt.Sprintf("top1/top2 margin %.2f is below %.2f", decision.Top1Top2Margin, convergenceMinTopGap)
	default:
		decision.Converged = true
		decision.Reason = "top hypothesis is separated from alternatives and key evidence is complete"
	}

	return decision
}

const convergenceMinConfidence = 0.75

func rankedHypotheses(scores []HypothesisScore) []HypothesisScore {
	ranked := append([]HypothesisScore(nil), scores...)
	sort.SliceStable(ranked, func(i, j int) bool {
		diff := ranked[i].Confidence - ranked[j].Confidence
		if math.Abs(diff) > primaryConfidenceTieEpsilon {
			return diff > 0
		}
		leftSpecificity := hypothesisSpecificityRank(ranked[i].Type)
		rightSpecificity := hypothesisSpecificityRank(ranked[j].Type)
		if leftSpecificity != rightSpecificity {
			return leftSpecificity > rightSpecificity
		}
		if len(ranked[i].SupportingRefs) != len(ranked[j].SupportingRefs) {
			return len(ranked[i].SupportingRefs) > len(ranked[j].SupportingRefs)
		}
		return strings.Compare(ranked[i].Type, ranked[j].Type) < 0
	})
	return ranked
}

func keyEvidenceComplete(score HypothesisScore) bool {
	return len(score.SupportingRefs) > 0 && len(score.MissingEvidence) == 0
}

func highConfidenceTypes(ranked []HypothesisScore) []string {
	types := []string{}
	for _, score := range ranked {
		if score.Confidence < convergenceHighConfidenceFloor {
			continue
		}
		types = append(types, score.Type)
	}
	return types
}

func confirmedTypes(ranked []HypothesisScore, threshold float64) []string {
	types := []string{}
	for _, score := range ranked {
		if score.Status == "" && score.Confidence >= threshold {
			types = append(types, score.Type)
			continue
		}
		if score.Status == "confirmed" {
			types = append(types, score.Type)
		}
	}
	return types
}

func distinguishingEvidence(ranked []HypothesisScore) []string {
	items := []string{}
	for _, score := range ranked {
		if score.Confidence < convergenceHighConfidenceFloor {
			continue
		}
		if len(score.MissingEvidence) > 0 {
			items = append(items, score.MissingEvidence...)
			continue
		}
		items = append(items, defaultDistinguishingEvidence(score.Type)...)
	}
	return appendUniqueStrings(items)
}

func defaultDistinguishingEvidence(hypothesisType string) []string {
	switch hypothesisType {
	case "memory_limit_too_low":
		return []string{"memory metrics", "container termination status"}
	case "application_memory_leak":
		return []string{"memory metrics", "heap/profile or sustained memory growth evidence"}
	case "node_memory_pressure":
		return []string{"node pressure observation", "topology context"}
	case "bad_config":
		return []string{"logs or events mentioning configuration errors"}
	case "missing_secret_or_configmap":
		return []string{"secret/configmap reference evidence", "events"}
	case "dependency_unavailable":
		return []string{"dependency timeout/refused log evidence", "events"}
	case "probe_misconfigured":
		return []string{"probe events and probe spec", "logs"}
	case "pvc_unbound":
		return []string{"pvc status", "scheduler events"}
	case "scheduling_constraint":
		return []string{"scheduler events", "topology constraints"}
	case "image_pull_failed":
		return []string{"image pull events", "registry authentication details"}
	case "init_container_crash":
		return []string{"init container status and logs", "events"}
	case "node_eviction":
		return []string{"eviction events and node pressure conditions"}
	case "node_not_ready":
		return []string{"node Ready condition and pressure events"}
	default:
		return []string{"events", "logs", "topology context"}
	}
}
