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
	recentObservationsKeep           = 5
	dedupTimeWindow                  = 2 * time.Second
	dedupContentSimilarityThreshold  = 0.7
)

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

// CompressObservations compresses observation records for prompt size control.
func CompressObservations(observations []ObservationRecord) []string {
	if len(observations) <= maxObservationsBeforeCompression {
		return formatObservations(observations)
	}
	recent := observations[len(observations)-recentObservationsKeep:]
	older := observations[:len(observations)-recentObservationsKeep]
	result := make([]string, 0, len(observations))
	result = append(result, compressByTool(older)...)
	result = append(result, formatObservations(recent)...)
	return result
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

func compressByTool(observations []ObservationRecord) []string {
	if len(observations) == 0 {
		return nil
	}
	type toolGroup struct {
		toolName string
		count    int
		last     *ObservationRecord
	}
	groups := make(map[string]*toolGroup)
	order := make([]string, 0)
	for _, obs := range observations {
		tool := canonicalToolName(obs.ToolName)
		g, exists := groups[tool]
		if !exists {
			g = &toolGroup{toolName: obs.ToolName, count: 0}
			groups[tool] = g
			order = append(order, tool)
		}
		g.count++
		g.last = &obs
	}
	result := make([]string, 0, len(groups))
	for _, tool := range order {
		g := groups[tool]
		if g.count == 1 {
			result = append(result, formatSingleObservation(*g.last))
		} else {
			result = append(result, fmt.Sprintf("%s: %d calls, last: %s",
				g.toolName, g.count, formatObservationBrief(*g.last)))
		}
	}
	return result
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

func formatObservationBrief(obs ObservationRecord) string {
	if obs.Observation != "" {
		return truncateText(obs.Observation, 120)
	}
	if obs.Error != "" {
		return "error=" + truncateText(obs.Error, 120)
	}
	return "(no observation)"
}

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
