package llm

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"kubesage/internal/config"
)

const (
	GroundingDecisionPassed   = "passed"
	GroundingDecisionDegraded = "degraded"
	GroundingDecisionRejected = "rejected"
)

type GroundingOptions struct {
	SemanticMinOverlap   float64
	PassRiskThreshold    float64
	WarningRiskThreshold float64
	RejectRiskThreshold  float64
}

type GroundingValidation struct {
	Decision          string           `json:"decision"`
	HallucinationRisk float64          `json:"hallucination_risk"`
	GroundingScore    float64          `json:"grounding_score"`
	AgreementScore    float64          `json:"agreement_score"`
	UngroundedClaims  int              `json:"ungrounded_claims"`
	InvalidReferences []string         `json:"invalid_references,omitempty"`
	Warnings          []string         `json:"warnings,omitempty"`
	Summary           *GroundedSummary `json:"summary,omitempty"`
}

type EvidenceValidator struct {
	options  GroundingOptions
	evidence map[string]GroundingEvidence
}

func NewEvidenceValidator(evidence []GroundingEvidence, options GroundingOptions) *EvidenceValidator {
	options = normalizeGroundingOptions(options)
	byID := make(map[string]GroundingEvidence, len(evidence))
	for _, item := range evidence {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		byID[id] = item
	}
	return &EvidenceValidator{options: options, evidence: byID}
}

func NewEvidenceValidatorFromConfig(evidence []GroundingEvidence, cfg config.LLMGroundingConfig) *EvidenceValidator {
	return NewEvidenceValidator(evidence, GroundingOptions{
		SemanticMinOverlap:   cfg.SemanticMinOverlap,
		PassRiskThreshold:    cfg.PassRiskThreshold,
		WarningRiskThreshold: cfg.WarningRiskThreshold,
		RejectRiskThreshold:  cfg.RejectRiskThreshold,
	})
}

func (v *EvidenceValidator) Validate(summary *GroundedSummary) GroundingValidation {
	if summary == nil {
		return GroundingValidation{
			Decision:          GroundingDecisionRejected,
			HallucinationRisk: 1,
			Warnings:          []string{"grounded summary is empty"},
		}
	}
	cleaned := *summary
	cleaned.EvidenceChain = nil
	cleaned.QualityWarnings = append([]string(nil), summary.QualityWarnings...)

	invalidRefs := []string{}
	ungrounded := 0
	scores := []float64{}

	rootScore, rootUngrounded, rootInvalid := v.scoreClaim(summary.RootCauseConfirmation.Summary, summary.RootCauseConfirmation.EvidenceRefs)
	if rootUngrounded {
		ungrounded++
		cleaned.QualityWarnings = append(cleaned.QualityWarnings, "root cause confirmation is weakly grounded")
	}
	invalidRefs = append(invalidRefs, rootInvalid...)
	scores = append(scores, rootScore)
	cleaned.RootCauseConfirmation.EvidenceRefs = validRefs(summary.RootCauseConfirmation.EvidenceRefs, v.evidence)

	for _, claim := range summary.EvidenceChain {
		score, isUngrounded, invalid := v.scoreClaim(claim.Claim, claim.EvidenceRefs)
		invalidRefs = append(invalidRefs, invalid...)
		refs := validRefs(claim.EvidenceRefs, v.evidence)
		if len(refs) == 0 {
			ungrounded++
			continue
		}
		claim.EvidenceRefs = refs
		claim.GroundingScore = roundScore(score)
		claim.Verified = score >= v.options.SemanticMinOverlap
		if isUngrounded {
			ungrounded++
			cleaned.QualityWarnings = append(cleaned.QualityWarnings, fmt.Sprintf("claim is weakly grounded: %s", claim.Claim))
		}
		cleaned.EvidenceChain = append(cleaned.EvidenceChain, claim)
		scores = append(scores, score)
	}

	groundingScore := average(scores)
	agreementScore := agreementScore(summary.RootCauseConfirmation.AgreementLevel)
	risk := 1 - (0.6*groundingScore + 0.4*agreementScore)
	risk = clamp01(risk)
	decision := GroundingDecisionPassed
	if risk > v.options.RejectRiskThreshold {
		decision = GroundingDecisionRejected
	} else if risk > v.options.WarningRiskThreshold {
		decision = GroundingDecisionDegraded
		cleaned.QualityWarnings = append(cleaned.QualityWarnings, fmt.Sprintf("hallucination risk %.2f exceeds warning threshold %.2f", risk, v.options.WarningRiskThreshold))
	}
	invalidRefs = uniqueSorted(invalidRefs)
	if len(invalidRefs) > 0 {
		cleaned.QualityWarnings = append(cleaned.QualityWarnings, "invalid evidence refs removed: "+strings.Join(invalidRefs, ","))
	}
	return GroundingValidation{
		Decision:          decision,
		HallucinationRisk: roundScore(risk),
		GroundingScore:    roundScore(groundingScore),
		AgreementScore:    roundScore(agreementScore),
		UngroundedClaims:  ungrounded,
		InvalidReferences: invalidRefs,
		Warnings:          cleaned.QualityWarnings,
		Summary:           &cleaned,
	}
}

func (v *EvidenceValidator) scoreClaim(claim string, refs []string) (float64, bool, []string) {
	refs = trimStrings(refs)
	if len(refs) == 0 {
		return 0, true, nil
	}
	valid := make([]GroundingEvidence, 0, len(refs))
	invalid := []string{}
	for _, ref := range refs {
		item, ok := v.evidence[ref]
		if !ok {
			invalid = append(invalid, ref)
			continue
		}
		valid = append(valid, item)
	}
	if len(valid) == 0 {
		return 0, true, invalid
	}
	evidenceText := strings.Builder{}
	for _, item := range valid {
		evidenceText.WriteString(item.SourceType)
		evidenceText.WriteString(" ")
		evidenceText.WriteString(item.Title)
		evidenceText.WriteString(" ")
		evidenceText.WriteString(item.Content)
		evidenceText.WriteString(" ")
	}
	score := jaccard(claim, evidenceText.String())
	return score, score < v.options.SemanticMinOverlap, invalid
}

func normalizeGroundingOptions(options GroundingOptions) GroundingOptions {
	if options.SemanticMinOverlap <= 0 {
		options.SemanticMinOverlap = 0.3
	}
	if options.PassRiskThreshold <= 0 {
		options.PassRiskThreshold = 0.2
	}
	if options.WarningRiskThreshold <= 0 {
		options.WarningRiskThreshold = 0.3
	}
	if options.RejectRiskThreshold <= 0 {
		options.RejectRiskThreshold = 0.4
	}
	return options
}

func agreementScore(level string) float64 {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "full_agree":
		return 1
	case "partial_agree":
		return 0.6
	case "disagree":
		return 0
	default:
		return 0
	}
}

func validRefs(refs []string, evidence map[string]GroundingEvidence) []string {
	result := []string{}
	for _, ref := range trimStrings(refs) {
		if _, ok := evidence[ref]; ok {
			result = append(result, ref)
		}
	}
	return result
}

func trimStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

var tokenPattern = regexp.MustCompile(`[a-z0-9]+`)

func jaccard(left, right string) float64 {
	a := tokenSet(left)
	b := tokenSet(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for token := range a {
		if _, ok := b[token]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func tokenSet(text string) map[string]struct{} {
	text = strings.ToLower(text)
	tokens := tokenPattern.FindAllString(text, -1)
	result := map[string]struct{}{}
	for _, token := range tokens {
		if len(token) < 3 || stopWords[token] {
			continue
		}
		result[token] = struct{}{}
	}
	return result
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "that": true, "this": true,
	"was": true, "were": true, "are": true, "pod": true, "container": true, "because": true,
	"has": true, "have": true, "into": true, "near": true, "state": true, "status": true,
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func roundScore(value float64) float64 {
	return math.Round(value*1000) / 1000
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
