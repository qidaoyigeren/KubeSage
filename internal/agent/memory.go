package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

// DiagnosisMemoryStore provides cross-session memory for the KubeSage agent.
// It stores insights from historical diagnoses and retrieves them for similar
// future faults, enabling the agent to leverage past experience.
type DiagnosisMemoryStore struct {
	repo MemoryRepository
}

// MemoryRepository abstracts the database layer for diagnosis memories.
type MemoryRepository interface {
	CreateMemory(ctx context.Context, m *model.DiagnosisMemory) error
	SearchSimilar(ctx context.Context, namespace, faultType, podPrefix string, limit int) ([]model.DiagnosisMemory, error)
	UpdateHit(ctx context.Context, id uint) error
	BatchUpdateHits(ctx context.Context, ids []uint) error
	FindByTaskID(ctx context.Context, taskID uint) (*model.DiagnosisMemory, error)
	ListByFaultType(ctx context.Context, faultType string, limit int) ([]model.DiagnosisMemory, error)
}

// NewDiagnosisMemoryStore creates a new memory store.
func NewDiagnosisMemoryStore(repo MemoryRepository) *DiagnosisMemoryStore {
	if repo == nil {
		return nil
	}
	return &DiagnosisMemoryStore{repo: repo}
}

// NormalizePodName extracts the workload-level prefix from a Kubernetes Pod name.
// Handles Deployment (ReplicaSet suffix), StatefulSet (ordinal), DaemonSet (single
// hash), and CronJob (timestamp + hash) naming conventions.
//
// Examples:
//
//	payment-service-7d4f8b9c6-xk2j → payment-service     (Deployment)
//	redis-statefulset-0             → redis-statefulset   (StatefulSet)
//	fluentd-ds-abc12                → fluentd-ds          (DaemonSet)
//	cleanup-cron-1718208000-xk2j    → cleanup-cron        (CronJob)
func NormalizePodName(podName string) string {
	if podName == "" {
		return ""
	}

	// StatefulSet pattern: <name>-<ordinal>
	if matched, prefix := stripStatefulSetOrdinal(podName); matched {
		return prefix
	}

	// CronJob pattern: <name>-<unix-timestamp>-<pod-hash>
	// Timestamps are 10-digit unix seconds.
	if matched, prefix := stripCronJobSuffix(podName); matched {
		return prefix
	}

	// Deployment (ReplicaSet) pattern: <name>-<rs-hash>-<pod-hash>
	if prefix := stripReplicaSetSuffix(podName); prefix != podName {
		return prefix
	}

	// DaemonSet pattern: <name>-<single-hash> (5 chars, alphanumeric)
	if prefix := stripDaemonSetSuffix(podName); prefix != podName {
		return prefix
	}

	return podName
}

// stripReplicaSetSuffix handles the Deployment → ReplicaSet → Pod naming convention.
// Pattern: <deploy-name>-<rs-hash>-<pod-hash>
// The ReplicaSet hash is typically 9-10 chars of [a-z0-9], Pod hash is 5 chars.
func stripReplicaSetSuffix(name string) string {
	// Match: <prefix>-<alphanum>-[alphanum]{5,}
	// We look for two consecutive hash segments after the workload name.
	re := regexp.MustCompile(`^(.+)-[a-z0-9]{6,10}-[a-z0-9]{5}$`)
	if matches := re.FindStringSubmatch(name); len(matches) >= 2 {
		return matches[1]
	}

	// Simpler fallback: strip the last two dash-separated segments if they look like hashes.
	parts := strings.Split(name, "-")
	if len(parts) >= 3 {
		lastIsHash := regexp.MustCompile(`^[a-z0-9]{5,10}$`).MatchString(parts[len(parts)-1])
		secondLastIsHash := regexp.MustCompile(`^[a-z0-9]{5,10}$`).MatchString(parts[len(parts)-2])
		if lastIsHash && secondLastIsHash {
			return strings.Join(parts[:len(parts)-2], "-")
		}
	}

	return name
}

// stripStatefulSetOrdinal handles the StatefulSet naming convention.
// Pattern: <statefulset-name>-<ordinal>
func stripStatefulSetOrdinal(name string) (bool, string) {
	re := regexp.MustCompile(`^(.+)-(\d+)$`)
	matches := re.FindStringSubmatch(name)
	if len(matches) == 3 {
		return true, matches[1]
	}
	return false, name
}

// stripCronJobSuffix handles the CronJob naming convention.
// Pattern: <cronjob-name>-<unix-timestamp(10 digits)>-<pod-hash(5 chars)>
func stripCronJobSuffix(name string) (bool, string) {
	re := regexp.MustCompile(`^(.+)-(\d{10})-[a-z0-9]{5}$`)
	matches := re.FindStringSubmatch(name)
	if len(matches) == 3 {
		return true, matches[1]
	}
	return false, name
}

// stripDaemonSetSuffix handles the DaemonSet naming convention.
// Pattern: <daemonset-name>-<hash(5 chars)>
func stripDaemonSetSuffix(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) >= 2 {
		last := parts[len(parts)-1]
		if regexp.MustCompile(`^[a-z0-9]{5}$`).MatchString(last) {
			return strings.Join(parts[:len(parts)-1], "-")
		}
	}
	return name
}

// SearchSimilar finds historical diagnosis memories that match the current fault.
// It searches by namespace + fault_type + workload prefix, returning the most
// relevant memories sorted by confidence and recency.
func (s *DiagnosisMemoryStore) SearchSimilar(
	ctx context.Context,
	namespace, faultType, podName string,
	limit int,
) ([]model.DiagnosisMemory, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}

	podPrefix := NormalizePodName(podName)
	memories, err := s.repo.SearchSimilar(ctx, namespace, faultType, podPrefix, limit)
	if err != nil {
		return nil, fmt.Errorf("search similar memories: %w", err)
	}

	// Update hit counts for returned memories.
	for _, m := range memories {
		_ = s.repo.UpdateHit(ctx, m.ID)
	}

	return memories, nil
}

// Save creates a new diagnosis memory record from a completed diagnostic run.
// It extracts the most important evidence and tools, normalizes the pod name,
// and stores the result for future retrieval.
func (s *DiagnosisMemoryStore) Save(
	ctx context.Context,
	taskID uint,
	namespace, podName, faultType, rootCause string,
	confidence float64,
	evidences []diagnostic.EvidenceRecord,
	toolNames []string,
) error {
	if s == nil || s.repo == nil {
		return nil
	}

	// Only save if confidence meets the threshold.
	if confidence < 0.7 {
		return nil
	}

	// Extract key evidence: top-N by severity, sorted.
	keyEvidence := extractKeyEvidence(evidences, 10)
	effectiveTools := extractEffectiveTools(toolNames)

	memory := &model.DiagnosisMemory{
		Namespace:      namespace,
		PodNamePrefix:  NormalizePodName(podName),
		FaultType:      faultType,
		RootCause:      rootCause,
		KeyEvidence:    keyEvidence,
		EffectiveTools: effectiveTools,
		Confidence:     confidence,
		TaskID:         taskID,
	}

	return s.repo.CreateMemory(ctx, memory)
}

// UpdateFromFeedback enhances an existing memory with human feedback data.
// When a human corrects the root cause or rates the diagnosis as not useful,
// the memory is updated to reflect the corrected information.
func (s *DiagnosisMemoryStore) UpdateFromFeedback(
	ctx context.Context,
	taskID uint,
	feedback *model.DiagnosisFeedback,
) error {
	if s == nil || s.repo == nil || feedback == nil {
		return nil
	}

	// Find the existing memory by task ID.
	memory, err := s.repo.FindByTaskID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("find memory for feedback update: %w", err)
	}
	if memory == nil {
		return nil
	}

	memory.FeedbackRating = feedback.Rating
	if feedback.Rating == model.FeedbackRatingNotUseful && feedback.CorrectedRootCause != "" {
		memory.CorrectedCause = feedback.CorrectedRootCause
	}
	return s.repo.CreateMemory(ctx, memory)
}

// FormatHistoricalContext formats historical diagnosis memories as text for
// injection into LLM prompts. It provides the LLM with relevant past experience.
func FormatHistoricalContext(memories []model.DiagnosisMemory) string {
	if len(memories) == 0 {
		return ""
	}

	var parts []string
	parts = append(parts, "HISTORICAL CONTEXT — Similar past diagnoses:")

	for i, m := range memories {
		entry := fmt.Sprintf("  [%d] Fault: %s | Root Cause: %s | Confidence: %.0f%%",
			i+1, m.FaultType, m.RootCause, m.Confidence*100)

		if m.CorrectedCause != "" {
			entry += fmt.Sprintf(" | CORRECTED: %s", m.CorrectedCause)
		}
		if m.FeedbackRating != "" {
			entry += fmt.Sprintf(" | Feedback: %s", m.FeedbackRating)
		}

		// Add top effective tools if available.
		if len(m.EffectiveTools) > 0 {
			tools := extractStringSlice(m.EffectiveTools)
			if len(tools) > 0 {
				entry += fmt.Sprintf(" | Helpful tools: %s", strings.Join(tools[:minInt(3, len(tools))], ", "))
			}
		}

		parts = append(parts, entry)
	}

	return strings.Join(parts, "\n")
}

// AggregateHelpfulTools returns the most frequently helpful tools for a given
// fault type, based on historical feedback.
func (s *DiagnosisMemoryStore) AggregateHelpfulTools(
	ctx context.Context,
	faultType string,
	limit int,
) ([]string, error) {
	if s == nil || s.repo == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 5
	}

	memories, err := s.repo.ListByFaultType(ctx, faultType, limit*2)
	if err != nil {
		return nil, err
	}

	// Count tool occurrences.
	toolCounts := make(map[string]int)
	for _, m := range memories {
		tools := extractStringSlice(m.EffectiveTools)
		for _, tool := range tools {
			toolCounts[tool]++
		}
	}

	// Sort by count descending and take top-N.
	type toolRank struct {
		name  string
		count int
	}
	ranked := make([]toolRank, 0, len(toolCounts))
	for name, count := range toolCounts {
		ranked = append(ranked, toolRank{name, count})
	}
	// Simple bubble sort for small slices (n <= 20 typically).
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].count > ranked[i].count {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}

	result := make([]string, 0, limit)
	for i := 0; i < limit && i < len(ranked); i++ {
		result = append(result, ranked[i].name)
	}
	return result, nil
}

// --- internal helpers ---

// extractKeyEvidence selects the most important evidence records for storage.
func extractKeyEvidence(evidences []diagnostic.EvidenceRecord, maxItems int) model.JSONText {
	if len(evidences) == 0 {
		return ""
	}

	// Sort by severity.
	sortable := make([]diagnostic.EvidenceRecord, len(evidences))
	copy(sortable, evidences)
	for i := 0; i < len(sortable); i++ {
		for j := i + 1; j < len(sortable); j++ {
			if severityRankEvidence(sortable[j]) > severityRankEvidence(sortable[i]) {
				sortable[i], sortable[j] = sortable[j], sortable[i]
			}
		}
	}

	if len(sortable) > maxItems {
		sortable = sortable[:maxItems]
	}

	// Convert to a simple JSON-serializable format.
	type evidenceBrief struct {
		Title    string `json:"title"`
		Severity string `json:"severity"`
		Source   string `json:"source"`
		Content  string `json:"content"`
	}
	items := make([]evidenceBrief, len(sortable))
	for i, e := range sortable {
		content := e.Content
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		items[i] = evidenceBrief{
			Title:    e.Title,
			Severity: e.Severity,
			Source:   e.SourceType,
			Content:  content,
		}
	}

	bytes, _ := json.Marshal(items)
	return model.JSONText(bytes)
}

// extractEffectiveTools converts tool names to a JSON-serializable format.
func extractEffectiveTools(toolNames []string) model.JSONText {
	bytes, _ := json.Marshal(toolNames)
	return model.JSONText(bytes)
}

// extractStringSlice extracts a []string from JSONText.
func extractStringSlice(jt model.JSONText) []string {
	if len(jt) == 0 {
		return nil
	}
	var result []string
	if err := json.Unmarshal([]byte(string(jt)), &result); err != nil {
		return nil
	}
	return result
}

func severityRankEvidence(e diagnostic.EvidenceRecord) int {
	switch strings.ToLower(e.Severity) {
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
