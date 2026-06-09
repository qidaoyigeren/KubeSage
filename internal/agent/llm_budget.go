package agent

import (
	"context"
	"sync"
)

const defaultMaxLLMCompletionTokensPerCall = 2048

// ModelProfile contains model-specific token budget settings.
type ModelProfile struct {
	MaxTokens           int
	ContextWindow       int
	MaxCompletionTokens int
}

// Built-in model profiles. When a model name is not found, a conservative
// default is used.
var builtinModelProfiles = map[string]ModelProfile{
	"deepseek-chat":     {MaxTokens: 12000, ContextWindow: 32768, MaxCompletionTokens: 4096},
	"deepseek-reasoner": {MaxTokens: 12000, ContextWindow: 65536, MaxCompletionTokens: 4096},
	"gpt-4o":            {MaxTokens: 30000, ContextWindow: 128000, MaxCompletionTokens: 16384},
	"gpt-4o-mini":       {MaxTokens: 15000, ContextWindow: 128000, MaxCompletionTokens: 16384},
	"claude-sonnet-4-6": {MaxTokens: 40000, ContextWindow: 200000, MaxCompletionTokens: 16384},
	"claude-opus-4-8":   {MaxTokens: 40000, ContextWindow: 200000, MaxCompletionTokens: 16384},
	"claude-haiku-4-5":  {MaxTokens: 15000, ContextWindow: 200000, MaxCompletionTokens: 8192},
}

// Phase token allocation fractions. Total must sum to ≤ 1.0.
// Reserve is the leftover budget for fallback and unexpected calls.
const (
	PhaseAllocPlan           = 0.15
	PhaseAllocAdjustment     = 0.15
	PhaseAllocReflection     = 0.30
	PhaseAllocFinalDiagnosis = 0.25
	PhaseAllocReserve        = 0.15
)

// GetModelProfile returns the token profile for a given model name.
// Falls back to a conservative profile for unknown models.
func GetModelProfile(modelName string) ModelProfile {
	if profile, ok := builtinModelProfiles[modelName]; ok {
		return profile
	}
	// Conservative default for unknown models.
	return ModelProfile{
		MaxTokens:           12000,
		ContextWindow:       32768,
		MaxCompletionTokens: 4096,
	}
}

// TokenBudgetForPhase returns the recommended token allowance for a diagnostic
// phase. This is advisory — the agent may exceed it for critical phases.
func TokenBudgetForPhase(totalBudget int, phaseFraction float64) int {
	if totalBudget <= 0 {
		return 0
	}
	allowance := int(float64(totalBudget) * phaseFraction)
	if allowance <= 0 {
		allowance = totalBudget / 6 // reasonable floor
	}
	return allowance
}

// RegisterModelProfile adds a custom model profile. Must be called before
// WithLLMTokenBudget to take effect.
func RegisterModelProfile(name string, profile ModelProfile) {
	builtinModelProfiles[name] = profile
}

// EstimateTokens provides a language-aware token count estimate.
// - Chinese / CJK characters: ≈ 2 tokens each
// - English words: ≈ 1.3 tokens each
// - JSON structural characters: ≈ 1 token each
// Falls back to len(text)/3 when uncertain.
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}

	var cjk, alpha, numeric, structural, other int
	for _, r := range text {
		switch {
		case r >= 0x4E00 && r <= 0x9FFF || r >= 0x3400 && r <= 0x4DBF ||
			r >= 0x20000 && r <= 0x2A6DF || r >= 0xF900 && r <= 0xFAFF:
			// CJK Unified Ideographs
			cjk++
		case r >= 0x3040 && r <= 0x30FF || r >= 0xAC00 && r <= 0xD7AF:
			// Japanese Kana + Korean Hangul
			cjk++
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			alpha++
		case r >= '0' && r <= '9':
			numeric++
		case r == '{' || r == '}' || r == '[' || r == ']' || r == '"' ||
			r == ':' || r == ',' || r == '\\':
			structural++
		default:
			if r <= 127 {
				other++
			} else {
				// Non-CJK multibyte: treat as ~2 tokens.
				cjk++
			}
		}
	}

	// Approximate tokens:
	est := 0
	est += cjk * 2                       // CJK: ~2 tokens each
	est += int(float64(alpha)*1.3 + 0.5) // English letters: ~1.3 tokens per word avg
	est += numeric                       // Digits: ~1 token each
	est += structural                    // JSON structure: ~1 token each
	est += other                         // Other ASCII: ~1 token each

	// Floor at len/3 as a safety net for very short mixed text.
	floor := len(text) / 3
	if est < floor {
		est = floor
	}
	if est <= 0 {
		est = 1
	}
	return est
}

type llmTokenBudgetContextKey struct{}

type LLMTokenBudget struct {
	mu                  sync.Mutex
	maxTokens           int
	usedTokens          int
	maxCompletionTokens int
	modelProfile        *ModelProfile
	// phaseUsed tracks token usage per phase for observability.
	phaseUsed map[string]int
}

// WithLLMTokenBudget attaches a run-local token budget to the context. The
// budget is intentionally not stored on the shared LLM client so concurrent
// diagnoses cannot consume each other's allowance.
// modelName, when provided, configures the budget with model-specific defaults.
func WithLLMTokenBudget(ctx context.Context, maxTokens int, modelName ...string) context.Context {
	if maxTokens <= 0 {
		return ctx
	}
	budget := &LLMTokenBudget{
		maxTokens:           maxTokens,
		maxCompletionTokens: defaultMaxLLMCompletionTokensPerCall,
		phaseUsed:           make(map[string]int),
	}
	if len(modelName) > 0 && modelName[0] != "" {
		profile := GetModelProfile(modelName[0])
		budget.modelProfile = &profile
		if profile.MaxCompletionTokens > 0 {
			budget.maxCompletionTokens = profile.MaxCompletionTokens
		}
	}
	return context.WithValue(ctx, llmTokenBudgetContextKey{}, budget)
}

func llmTokenBudgetFromContext(ctx context.Context) *LLMTokenBudget {
	if ctx == nil {
		return nil
	}
	budget, _ := ctx.Value(llmTokenBudgetContextKey{}).(*LLMTokenBudget)
	return budget
}

// RemainingLLMTokens returns the current run's remaining total tokens and the
// maximum completion size for one call.
func RemainingLLMTokens(ctx context.Context) (remaining, maxCompletion int, limited bool) {
	budget := llmTokenBudgetFromContext(ctx)
	if budget == nil {
		return 0, 0, false
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	remaining = budget.maxTokens - budget.usedTokens
	if remaining < 0 {
		remaining = 0
	}
	return remaining, budget.maxCompletionTokens, true
}

// RecordLLMTokenUsage records provider-reported usage against the current run.
func RecordLLMTokenUsage(ctx context.Context, tokens int) {
	if tokens <= 0 {
		return
	}
	budget := llmTokenBudgetFromContext(ctx)
	if budget == nil {
		return
	}
	budget.mu.Lock()
	budget.usedTokens += tokens
	budget.mu.Unlock()
}

// RecordPhaseTokenUsage records token usage for a specific diagnostic phase.
// This enables phase-level budget tracking and observability.
func RecordPhaseTokenUsage(ctx context.Context, phase string, tokens int) {
	if tokens <= 0 {
		return
	}
	budget := llmTokenBudgetFromContext(ctx)
	if budget == nil {
		return
	}
	budget.mu.Lock()
	budget.usedTokens += tokens
	if budget.phaseUsed != nil {
		budget.phaseUsed[phase] += tokens
	}
	budget.mu.Unlock()
}

// PhaseTokenUsage returns the current per-phase token breakdown.
func PhaseTokenUsage(ctx context.Context) map[string]int {
	budget := llmTokenBudgetFromContext(ctx)
	if budget == nil {
		return nil
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	result := make(map[string]int, len(budget.phaseUsed))
	for k, v := range budget.phaseUsed {
		result[k] = v
	}
	return result
}

// ExhaustLLMTokenBudget closes the current run's budget after a request is
// rejected because its prompt cannot fit in the remaining allowance.
func ExhaustLLMTokenBudget(ctx context.Context) {
	budget := llmTokenBudgetFromContext(ctx)
	if budget == nil {
		return
	}
	budget.mu.Lock()
	budget.usedTokens = budget.maxTokens
	budget.mu.Unlock()
}

func LLMTokenUsage(ctx context.Context) int {
	budget := llmTokenBudgetFromContext(ctx)
	if budget == nil {
		return 0
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.usedTokens
}

func llmTokenBudgetAvailable(ctx context.Context) bool {
	remaining, _, limited := RemainingLLMTokens(ctx)
	return !limited || remaining > 0
}
