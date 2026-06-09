package agent

import (
	"context"
	"sync"
)

const defaultMaxLLMCompletionTokensPerCall = 2048

type llmTokenBudgetContextKey struct{}

type LLMTokenBudget struct {
	mu                  sync.Mutex
	maxTokens           int
	usedTokens          int
	maxCompletionTokens int
}

// WithLLMTokenBudget attaches a run-local token budget to the context. The
// budget is intentionally not stored on the shared LLM client so concurrent
// diagnoses cannot consume each other's allowance.
func WithLLMTokenBudget(ctx context.Context, maxTokens int) context.Context {
	if maxTokens <= 0 {
		return ctx
	}
	budget := &LLMTokenBudget{
		maxTokens:           maxTokens,
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
