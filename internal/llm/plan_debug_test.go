package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"kubesage/internal/agent"
	"kubesage/internal/config"
)

// TestDebugGenerateAgentPlanRaw gets the raw LLM response to inspect format issues
func TestDebugGenerateAgentPlanRaw(t *testing.T) {
	if os.Getenv("KUBESAGE_LLM_PLAN_DEBUG") != "1" {
		t.Skip("set KUBESAGE_LLM_PLAN_DEBUG=1 and KUBESAGE_LLM_API_KEY to run this live LLM debug test")
	}
	apiKey := os.Getenv("KUBESAGE_LLM_API_KEY")
	if apiKey == "" {
		t.Skip("KUBESAGE_LLM_API_KEY is required for live LLM debug test")
	}
	baseURL := os.Getenv("KUBESAGE_LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	model := os.Getenv("KUBESAGE_LLM_MODEL")
	if model == "" {
		model = "deepseek-chat"
	}
	cfg := config.LLMConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	}
	c := NewOpenAICompatibleClient(cfg).(*OpenAICompatibleClient)

	goal := agent.Goal{
		Namespace:     "eval-bank",
		PodName:       "crashloop-missing-config",
		ExpectedFault: "CrashLoopBackOff",
	}
	tools := []agent.ToolMetadata{
		{Name: "k8s.get_pod", Description: "Read pod status snapshot", ReadOnly: true, Critical: true},
		{Name: "k8s.get_events", Description: "Read Kubernetes events for a pod", ReadOnly: true, Critical: false},
		{Name: "k8s.get_logs", Description: "Read container logs (current and previous)", ReadOnly: true, Critical: false},
		{Name: "k8s.get_topology", Description: "Read workload topology and node context", ReadOnly: true, Critical: false},
		{Name: "runbook.search", Description: "Search diagnostic runbooks", ReadOnly: true, Critical: false},
		{Name: "loki.query_logs", Description: "Query Loki centralized logs", ReadOnly: true, Critical: false},
	}

	prompt := agent.PlanPrompt{
		Goal:  goal,
		Tools: tools,
		Safety: []string{
			"Use only tools present in the provided tool list.",
			"Use read-only tools only.",
			"Do not propose remediation execution.",
			"Prefer parallel evidence collection when tools are independent.",
		},
	}

	payloadBytes, _ := json.MarshalIndent(prompt, "", "  ")
	systemMsg := `You are KubeSage's Kubernetes RCA planner.
Return a JSON object with EXACTLY these fields:
  "plan_summary": string — one-sentence diagnosis strategy,
  "steps": array of objects, each with "tool_name" (string), "reason" (string), "critical" (bool), "input" (object),
  "expected_observations": array of strings,
  "stop_condition": array of strings.
Each step.tool_name MUST be one of the provided tool names exactly as listed.
Each step.input should contain namespace and pod_name from the goal.
Do not propose remediation execution or cluster mutation.
Return JSON only, no markdown fences.`

	t.Logf("System: %s", systemMsg)
	t.Logf("User: %s", string(payloadBytes))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	endpoint, _ := c.chatCompletionsURL()
	t.Logf("Endpoint: %s", endpoint)

	// Manually construct and send the request to see raw response
	payload := chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemMsg},
			{Role: "user", Content: string(payloadBytes)},
		},
		Temperature:    0.1,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	var raw chatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if raw.Error != nil {
		t.Fatalf("LLM error: %s: %s", raw.Error.Type, raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		t.Fatal("No choices in response")
	}

	content := raw.Choices[0].Message.Content
	t.Logf("Raw LLM response:\n%s", content)

	// Try to parse with flexible parser
	cleaned := stripMarkdownFences(content)
	plan, err := parseFlexiblePlan(cleaned, goal)
	if err != nil {
		t.Fatalf("parseFlexiblePlan failed: %v", err)
	}
	t.Logf("Parsed plan summary: %s", plan.Summary)
	t.Logf("Parsed plan steps: %d", len(plan.Steps))
	for i, step := range plan.Steps {
		t.Logf("  Step %d: id=%s tool=%s reason=%s critical=%v", i, step.ID, step.ToolName, step.Reason, step.Critical)
	}
	t.Logf("Stop conditions: %v", plan.StopCondition)
	t.Logf("Expected observations: %v", plan.ExpectedObservations)

	if len(plan.Steps) == 0 {
		t.Fatal("plan has 0 steps after flexible parsing")
	}
}

func stripFences(s string) string {
	s = trimSpace(s)
	if len(s) >= 7 && s[:7] == "```json" {
		s = s[7:]
	}
	if len(s) >= 3 && s[:3] == "```" {
		s = s[3:]
	}
	if len(s) >= 3 && s[len(s)-3:] == "```" {
		s = s[:len(s)-3]
	}
	return trimSpace(s)
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\r' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func keysOf(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func truncateValue(v interface{}, maxLen int) string {
	s := fmt.Sprintf("%v", v)
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}
