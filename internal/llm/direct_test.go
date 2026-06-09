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
	requestMaxTokens := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var request chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		requestMaxTokens = request.MaxTokens
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
	if requestMaxTokens <= 0 || requestMaxTokens > 1000 {
		t.Fatalf("request max_tokens = %d, want a positive bounded value", requestMaxTokens)
	}
	if got := agent.LLMTokenUsage(ctx); got != 120 {
		t.Fatalf("run-local token usage = %d, want 120", got)
	}
}

func TestLLMPreflightExhaustsBudgetWhenPromptCannotFit(t *testing.T) {
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

	if _, err := client.GenerateDirectDiagnosis(ctx, map[string]string{"pod": "oom"}); err == nil {
		t.Fatal("expected prompt preflight budget error")
	}
	if requests != 0 {
		t.Fatalf("budget-rejected prompt made %d HTTP requests", requests)
	}
	if remaining, _, _ := agent.RemainingLLMTokens(ctx); remaining != 0 {
		t.Fatalf("preflight-rejected run still has %d tokens remaining", remaining)
	}
}
