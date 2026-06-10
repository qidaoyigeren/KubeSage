package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kubesage/internal/agent"
	"kubesage/internal/config"
)

func TestGenerateDirectDiagnosisTracksUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{
				"message": map[string]string{
					"role":    "assistant",
					"content": `{"fault_type":"OOMKilled","root_cause_summary":"exitCode 137","confidence_score":0.9,"evidence":["reason=OOMKilled"],"suggested_actions":["inspect memory limit"],"risk_level":"medium","need_human_confirm":true}`,
				},
			}},
			"usage": map[string]int{
				"prompt_tokens":     100,
				"completion_tokens": 20,
				"total_tokens":      120,
			},
		})
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient(config.LLMConfig{
		Enabled: true,
		BaseURL: server.URL,
		APIKey:  "test",
		Model:   "test-model",
	}).(*OpenAICompatibleClient)

	ctx := agent.WithLLMTokenBudget(context.Background(), 1000)
	result, err := client.GenerateDirectDiagnosis(ctx, map[string]string{"pod": "oom"})
	if err != nil {
		t.Fatalf("GenerateDirectDiagnosis() error = %v", err)
	}
	if result.FaultType != "OOMKilled" {
		t.Fatalf("fault type = %q", result.FaultType)
	}
	if got := client.CumulativeUsage(); got.TotalTokens != 120 {
		t.Fatalf("total tokens = %d, want 120", got.TotalTokens)
	}
	if got := client.UsageCallCount(); got != 1 {
		t.Fatalf("usage calls = %d, want 1", got)
	}
	// Token budget is now observability-only and no longer sets MaxTokens.
	// The model's default context window controls completion size.
	if got := agent.LLMTokenUsage(ctx); got != 120 {
		t.Fatalf("run-local token usage = %d, want 120", got)
	}
}

func TestLLMTokenBudgetAllowsRequestWhenExhausted(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := NewOpenAICompatibleClient(config.LLMConfig{
		Enabled: true,
		BaseURL: server.URL,
		APIKey:  "test",
		Model:   "test-model",
	}).(*OpenAICompatibleClient)
	ctx := agent.WithLLMTokenBudget(context.Background(), 10)

	// Token budget no longer rejects requests. The call should proceed
	// to the server (and fail with 500, not a budget error).
	_, err := client.GenerateDirectDiagnosis(ctx, map[string]string{"pod": "oom"})
	if err == nil {
		t.Fatal("expected server error, not success")
	}
	if requests != 1 {
		t.Fatalf("expected 1 HTTP request (budget should not block), got %d", requests)
	}
}
