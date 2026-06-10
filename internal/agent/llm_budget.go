package agent

import (
	"context"
	"sync"
)

const defaultMaxLLMCompletionTokensPerCall = 2048

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

// LLMTokenBudget tracks per-run LLM token usage for observability. It no
// longer enforces hard limits — the model's context window and the agent's
// step/reflection budgets are the real resource controls.
type LLMTokenBudget struct {
	mu                  sync.Mutex
	maxCompletionTokens int
	usedTokens          int
}

// WithLLMTokenBudget attaches a run-local token usage tracker to the context.
// The tracker is per-run so concurrent diagnoses do not share counters.
func WithLLMTokenBudget(ctx context.Context, maxTokens int) context.Context {
	budget := &LLMTokenBudget{
		maxCompletionTokens: defaultMaxLLMCompletionTokensPerCall,
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

// RemainingLLMTokens returns the maximum completion tokens for one call and
// whether a tracker is present. The "remaining" total is no longer enforced
// but is still reported for observability.
func RemainingLLMTokens(ctx context.Context) (remaining, maxCompletion int, limited bool) {
	budget := llmTokenBudgetFromContext(ctx)
	if budget == nil {
		return 0, 0, false
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return 0, budget.maxCompletionTokens, true
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

// LLMTokenUsage returns the cumulative tokens used in the current run.
func LLMTokenUsage(ctx context.Context) int {
	budget := llmTokenBudgetFromContext(ctx)
	if budget == nil {
		return 0
	}
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.usedTokens
}
