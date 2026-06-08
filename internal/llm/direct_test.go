package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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

	result, err := client.GenerateDirectDiagnosis(context.Background(), map[string]string{"pod": "oom"})
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
}
