package agent

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"kubesage/internal/diagnostic"
)

// ScoredEvidence wraps an EvidenceRecord with a relevance score and reasons.
type ScoredEvidence struct {
	Record  diagnostic.EvidenceRecord
	Score   float64
	Reasons []string
}

// ScoredObservation wraps an ObservationRecord with a relevance score and reasons.
type ScoredObservation struct {
	Record  ObservationRecord
	Score   float64
	Reasons []string
}

// Severity weights for evidence scoring (defaults, overridable via config).
var defaultSeverityWeights = map[string]float64{
	"critical": 1.0,
	"error":    0.85,
	"warning":  0.6,
	"info":     0.3,
	"":         0.2,
}

// SourceType weights for evidence scoring (defaults, overridable via config).
var defaultSourceWeights = map[string]float64{
	"k8s_key_log":  1.0,
	"k8s_event":    0.85,
	"prometheus":   0.8,
	"runbook":      0.7,
	"k8s_topology": 0.5,
	"k8s_pod":      0.4,
	"k8s_pvc":      0.35,
	"agent":        0.3,
	"verification": 0.25,
}

const (
	maxObservationsBeforeCompression = 15
	topObservationsKeep              = 10 // keep top-N scored observations in full
	observationContentMaxLen         = 300
	dedupTimeWindow                  = 2 * time.Second
	dedupContentSimilarityThreshold  = 0.7
)

// Tool importance weights for observation scoring — observations from tools that
// surface definitive K8s signals are weighted higher than general-purpose tools.
var defaultObservationToolWeights = map[string]float64{
	"k8s.get_events":   0.9,
	"k8s.get_logs":     0.85,
	"k8s.get_pod":      0.8,
	"k8s.get_topology": 0.7,
	"k8s.get_pvc":      0.65,
	"k8s.config_refs":  0.6,
	"prometheus.query": 0.7,
	"loki.query":       0.7,
	"runbook.search":   0.5,
	"remediation":      0.3,
}

// EvidenceScorerConfig holds adjustable scoring parameters.
type EvidenceScorerConfig struct {
	TimeDecayHalfLife         float64
	RecencyBaseWeight         float64
	SeverityBaseWeight        float64
	SourceBaseWeight          float64
	HypothesisAlignmentWeight float64
	SeverityWeights           map[string]float64
	SourceWeights             map[string]float64
}

// DefaultEvidenceScorerConfig returns production-tested defaults.
func DefaultEvidenceScorerConfig() EvidenceScorerConfig {
	return EvidenceScorerConfig{
		TimeDecayHalfLife:         6.0,
		RecencyBaseWeight:         0.7,
		SeverityBaseWeight:        0.5,
		SourceBaseWeight:          0.6,
		HypothesisAlignmentWeight: 0.3,
	}
}

// EvidenceScorer scores and deduplicates evidence records.
type EvidenceScorer struct {
	cfg        EvidenceScorerConfig
	sevWeights map[string]float64
	srcWeights map[string]float64
}

// NewEvidenceScorer creates a scorer with default settings.
func NewEvidenceScorer() *EvidenceScorer {
	return NewEvidenceScorerWithConfig(DefaultEvidenceScorerConfig())
}

// NewEvidenceScorerWithConfig creates a scorer with custom weights.
func NewEvidenceScorerWithConfig(cfg EvidenceScorerConfig) *EvidenceScorer {
	if cfg.TimeDecayHalfLife <= 0 {
		cfg.TimeDecayHalfLife = 6.0
	}
	clampWeight(&cfg.RecencyBaseWeight, 0.7)
	clampWeight(&cfg.SeverityBaseWeight, 0.5)
	clampWeight(&cfg.SourceBaseWeight, 0.6)
	clampWeight(&cfg.HypothesisAlignmentWeight, 0.3)

	sevW := cfg.SeverityWeights
	if sevW == nil {
		sevW = defaultSeverityWeights
	}
	srcW := cfg.SourceWeights
	if srcW == nil {
		srcW = defaultSourceWeights
	}
	return &EvidenceScorer{cfg: cfg, sevWeights: sevW, srcWeights: srcW}
}

func clampWeight(w *float64, def float64) {
	if *w < 0 || *w > 1 {
		*w = def
	}
}

// ScoreEvidence assigns relevance scores based on recency, severity, source type,
// and hypothesis alignment.
func (s *EvidenceScorer) ScoreEvidence(
	records []diagnostic.EvidenceRecord,
	hypotheses []HypothesisScore,
	stepIndex int,
) []ScoredEvidence {
	if s == nil {
		return plainScored(records)
	}
	result := make([]ScoredEvidence, 0, len(records))
	for i, record := range records {
		score := 1.0
		var reasons []string

		age := float64(len(records) - 1 - i)
		recencyScore := math.Pow(0.5, age/s.cfg.TimeDecayHalfLife)
		score *= (1.0 - s.cfg.RecencyBaseWeight) + s.cfg.RecencyBaseWeight*recencyScore
		if age > 0 && recencyScore < 0.8 {
			reasons = append(reasons, fmt.Sprintf("recency_decay=%.2f", recencyScore))
		}

		sevW, ok := s.sevWeights[strings.ToLower(record.Severity)]
		if !ok {
			sevW = 0.2
		}
		score *= (1.0 - s.cfg.SeverityBaseWeight) + s.cfg.SeverityBaseWeight*sevW
		if sevW >= 0.8 {
			reasons = append(reasons, "high_severity="+record.Severity)
		}

		srcW, ok := s.srcWeights[record.SourceType]
		if !ok {
			srcW = 0.3
		}
		score *= (1.0 - s.cfg.SourceBaseWeight) + s.cfg.SourceBaseWeight*srcW
		if srcW >= 0.7 {
			reasons = append(reasons, "high_value_source="+record.SourceType)
		}

		for _, h := range hypotheses {
			if h.Confidence < 0.4 {
				continue
			}
			alignment := hypothesisEvidenceAlignment(h, record)
			if alignment > 0 {
				score *= 1.0 + alignment*s.cfg.HypothesisAlignmentWeight
				if alignment > 0.5 {
					reasons = append(reasons, fmt.Sprintf("aligns_with=%s(%.2f)", h.Type, h.Confidence))
				}
			}
		}

		if score > 1.0 {
			score = 1.0
		}
		if score < 0.0 {
			score = 0.0
		}
		result = append(result, ScoredEvidence{
			Record:  record,
			Score:   math.Round(score*1000) / 1000,
			Reasons: reasons,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	return result
}

// DeduplicateEvidence merges semantically similar evidence records.
func (s *EvidenceScorer) DeduplicateEvidence(records []diagnostic.EvidenceRecord) []diagnostic.EvidenceRecord {
	if len(records) <= 1 {
		return records
	}
	type dedupKey struct{ title string }
	seen := make(map[dedupKey]int)
	result := make([]diagnostic.EvidenceRecord, 0, len(records))
	for _, record := range records {
		key := dedupKey{title: normalizeTitle(record.Title)}
		if keptIdx, exists := seen[key]; exists {
			kept := &result[keptIdx]
			if severityRank(record.Severity) > severityRank(kept.Severity) {
				*kept = record
			} else if severityRank(record.Severity) == severityRank(kept.Severity) &&
				len(record.Content) > len(kept.Content) {
				*kept = record
			}
			if kept.Raw == nil && record.Raw != nil {
				kept.Raw = record.Raw
			}
			continue
		}
		seen[key] = len(result)
		result = append(result, record)
	}
	return result
}

// CompressObservations scores observations by relevance, keeps the top-N in
// full, and converts the rest to evidence records for optional LLM summarization.
// Returns formatted observation strings and low-relevance records for the caller
// to pass to an EvidenceSummarizer.
//
// When observations <= maxObservationsBeforeCompression, all are returned as-is
// with no remaining records.
func CompressObservations(observations []ObservationRecord) ([]string, []diagnostic.EvidenceRecord) {
	if len(observations) <= maxObservationsBeforeCompression {
		return formatObservations(observations), nil
	}

	scored := scoreObservations(observations)
	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })

	// Build result: top-N as formatted strings, rest as evidence records.
	var formatted []string
	var remaining []diagnostic.EvidenceRecord

	for i, so := range scored {
		if i < topObservationsKeep {
			formatted = append(formatted, formatScoredObservation(so))
		} else {
			remaining = append(remaining, observationToEvidenceRecord(so.Record))
		}
	}

	return formatted, remaining
}

// ScoreObservations scores observation records by relevance. Exported for tests.
func ScoreObservations(observations []ObservationRecord) []ScoredObservation {
	return scoreObservations(observations)
}

// scoreObservations assigns relevance scores to observation records based on:
//   - Error/success signal (failed + error text = highest weight)
//   - Evidence richness (has refs, has missing evidence)
//   - Tool importance (certain tools surface more definitive K8s signals)
//   - Content depth (very short success messages are low value)
//   - Recency decay (older observations gradually decay)
func scoreObservations(observations []ObservationRecord) []ScoredObservation {
	result := make([]ScoredObservation, len(observations))
	totalMinusOne := float64(len(observations) - 1)
	if totalMinusOne < 1 {
		totalMinusOne = 1
	}

	for i, obs := range observations {
		score := 0.5 // neutral baseline
		var reasons []string

		// --- error / success signal (0 to +0.40) ---
		if !obs.Success {
			if obs.Error != "" {
				score += 0.40
				reasons = append(reasons, "failed_with_error")
			} else {
				score += 0.25
				reasons = append(reasons, "failed")
			}
		}
		if obs.Success && len(obs.Warnings) > 0 {
			score += 0.10
			reasons = append(reasons, "has_warnings")
		}

		// --- evidence richness (0 to +0.25) ---
		if len(obs.EvidenceRefs) > 0 {
			score += 0.15
			reasons = append(reasons, "has_evidence_refs")
		}
		if len(obs.MissingEvidence) > 0 {
			score += 0.10
			reasons = append(reasons, "has_missing_evidence")
		}

		// --- tool importance (0 to +0.20) ---
		toolKey := canonicalToolName(obs.ToolName)
		if w, ok := defaultObservationToolWeights[toolKey]; ok {
			score += w * 0.20
			if w >= 0.8 {
				reasons = append(reasons, "high_value_tool="+toolKey)
			}
		}

		// --- content depth (0 to +0.15) ---
		obsLen := len(obs.Observation)
		switch {
		case obsLen > 200:
			score += 0.15
			reasons = append(reasons, "rich_content")
		case obsLen > 80:
			score += 0.08
		case obsLen <= 20 && obs.Success && obs.Error == "":
			// Trivial "done" messages are low value.
			score -= 0.10
			reasons = append(reasons, "trivial_content")
		}

		// --- recency decay (×0.85 to ×1.05) ---
		age := totalMinusOne - float64(i)
		recency := 1.0 - (age/totalMinusOne)*0.20 // Recent bias: newest +0.05, oldest -0.15
		score *= recency

		// Clamp.
		if score > 1.0 {
			score = 1.0
		}
		if score < 0.0 {
			score = 0.0
		}

		result[i] = ScoredObservation{
			Record:  obs,
			Score:   math.Round(score*1000) / 1000,
			Reasons: reasons,
		}
	}
	return result
}

// formatScoredObservation formats a single scored observation with its score.
func formatScoredObservation(so ScoredObservation) string {
	obs := so.Record
	if strings.TrimSpace(obs.Observation) == "" && obs.Error == "" {
		return ""
	}
	status := "success"
	if !obs.Success {
		status = "failed"
	}
	parts := []string{fmt.Sprintf("%s [%s|%.2f]: %s", obs.ToolName, status, so.Score, obs.Observation)}
	if len(obs.MissingEvidence) > 0 {
		parts = append(parts, "missing="+strings.Join(obs.MissingEvidence, ","))
	}
	if len(obs.Warnings) > 0 {
		parts = append(parts, "warnings="+strings.Join(obs.Warnings, ","))
	}
	if obs.Error != "" {
		parts = append(parts, "error="+obs.Error)
	}
	return strings.Join(parts, " ")
}

// observationToEvidenceRecord converts an observation to an evidence record so
// the low-relevance observations can be passed to SummarizeEvidences for LLM
// semantic compression.
func observationToEvidenceRecord(obs ObservationRecord) diagnostic.EvidenceRecord {
	severity := "info"
	if !obs.Success {
		if obs.Error != "" {
			severity = "error"
		} else {
			severity = "warning"
		}
	} else if len(obs.Warnings) > 0 {
		severity = "warning"
	}

	content := obs.Observation
	if content == "" {
		content = obs.Error
	}
	if len(obs.Warnings) > 0 {
		content += "\nwarnings: " + strings.Join(obs.Warnings, "; ")
	}
	if len(obs.MissingEvidence) > 0 {
		content += "\nmissing: " + strings.Join(obs.MissingEvidence, ", ")
	}
	if len(content) > observationContentMaxLen {
		content = content[:observationContentMaxLen] + "..."
	}

	return diagnostic.EvidenceRecord{
		SourceType: "tool_observation",
		Title:      obs.ToolName,
		Content:    content,
		Severity:   severity,
		Timestamp:  obs.Timestamp,
	}
}

// BuildEvidenceSummary builds a token-budget-aware evidence summary.
// Top-scoring records are included in full (truncated to maxContentLen).
// Remaining records are returned separately so the caller can summarize them
// via LLM or statistical grouping.
func BuildEvidenceSummary(scored []ScoredEvidence, maxRecords, maxContentLen int) (string, []diagnostic.EvidenceRecord) {
	if len(scored) == 0 {
		return "", nil
	}
	var parts []string
	var remaining []diagnostic.EvidenceRecord

	for i, se := range scored {
		if i < maxRecords {
			content := se.Record.Content
			if maxContentLen > 0 && len(content) > maxContentLen {
				content = content[:maxContentLen] + "..."
			}
			parts = append(parts, fmt.Sprintf("[%s|%.2f] %s: %s",
				se.Record.Severity, se.Score, se.Record.Title, content))
		} else {
			remaining = append(remaining, se.Record)
		}
	}

	// Add a statistical fallback summary for the remaining records.
	if len(remaining) > 0 {
		collapsedSevs := map[string]int{}
		for _, r := range remaining {
			collapsedSevs[r.Severity]++
		}
		sevSummary := make([]string, 0, len(collapsedSevs))
		for sev, count := range collapsedSevs {
			sevSummary = append(sevSummary, fmt.Sprintf("%s×%d", sev, count))
		}
		parts = append(parts, fmt.Sprintf("[collapsed] %d lower-relevance evidence records (%s) — awaiting LLM summary",
			len(remaining), strings.Join(sevSummary, ", ")))
	}
	return strings.Join(parts, "\n"), remaining
}

// TopScoredEvidence returns the top-N records by score.
func TopScoredEvidence(scored []ScoredEvidence, n int) []diagnostic.EvidenceRecord {
	if n <= 0 || n >= len(scored) {
		n = len(scored)
	}
	result := make([]diagnostic.EvidenceRecord, 0, n)
	for i := 0; i < n && i < len(scored); i++ {
		result = append(result, scored[i].Record)
	}
	return result
}

// --- package-level scorer (thread-safe) ---

var (
	globalScorer     *EvidenceScorer
	globalScorerOnce sync.Once
	globalScorerMu   sync.RWMutex
)

// GetEvidenceScorer returns the package-level scorer, initializing it once.
func GetEvidenceScorer() *EvidenceScorer {
	globalScorerOnce.Do(func() {
		globalScorer = NewEvidenceScorer()
	})
	globalScorerMu.RLock()
	defer globalScorerMu.RUnlock()
	return globalScorer
}

// SetEvidenceScorer replaces the package-level scorer (primarily for tests).
func SetEvidenceScorer(s *EvidenceScorer) {
	globalScorerMu.Lock()
	defer globalScorerMu.Unlock()
	globalScorer = s
}

// --- internal helpers ---

func plainScored(records []diagnostic.EvidenceRecord) []ScoredEvidence {
	result := make([]ScoredEvidence, len(records))
	for i, r := range records {
		score := 1.0 - float64(len(records)-1-i)*0.02
		if score < 0.1 {
			score = 0.1
		}
		result[i] = ScoredEvidence{Record: r, Score: score}
	}
	return result
}

func hypothesisEvidenceAlignment(h HypothesisScore, record diagnostic.EvidenceRecord) float64 {
	hypoText := strings.ToLower(h.Summary + " " + h.Type)
	evidenceText := strings.ToLower(record.Title + " " + truncateForAlignment(record.Content, 500))
	hypoWords := tokenizeKeywords(hypoText)
	if len(hypoWords) == 0 {
		return 0
	}
	matches := 0
	for _, word := range hypoWords {
		if strings.Contains(evidenceText, word) {
			matches++
		}
	}
	return float64(matches) / float64(len(hypoWords))
}

func tokenizeKeywords(text string) []string {
	words := strings.Fields(text)
	keywords := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?()[]{}\"'")
		if len(w) < 4 {
			continue
		}
		switch strings.ToLower(w) {
		case "that", "this", "with", "from", "have", "been", "were", "they", "will", "when", "what", "which":
			continue
		default:
			keywords = append(keywords, strings.ToLower(w))
		}
	}
	return keywords
}

func truncateForAlignment(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func normalizeTitle(title string) string {
	title = strings.TrimSpace(title)
	title = strings.Join(strings.Fields(title), " ")
	return strings.ToLower(title)
}

func severityRank(severity string) int {
	switch strings.ToLower(severity) {
	case "critical":
		return 4
	case "error":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

func formatObservations(observations []ObservationRecord) []string {
	result := make([]string, 0, len(observations))
	for _, obs := range observations {
		if text := formatSingleObservation(obs); text != "" {
			result = append(result, text)
		}
	}
	return result
}

func formatSingleObservation(obs ObservationRecord) string {
	if strings.TrimSpace(obs.Observation) == "" && obs.Error == "" {
		return ""
	}
	status := "success"
	if !obs.Success {
		status = "failed"
	}
	parts := []string{fmt.Sprintf("%s [%s]: %s", obs.ToolName, status, obs.Observation)}
	if len(obs.MissingEvidence) > 0 {
		parts = append(parts, "missing="+strings.Join(obs.MissingEvidence, ","))
	}
	if len(obs.Warnings) > 0 {
		parts = append(parts, "warnings="+strings.Join(obs.Warnings, ","))
	}
	if obs.Error != "" {
		parts = append(parts, "error="+obs.Error)
	}
	return strings.Join(parts, " ")
}
