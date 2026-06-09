package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DirectDiagnosis is the baseline output produced from a live evidence snapshot
// without deterministic analyzers or Agent planning.
type DirectDiagnosis struct {
	FaultType        string   `json:"fault_type"`
	RootCauseSummary string   `json:"root_cause_summary"`
	ConfidenceScore  float64  `json:"confidence_score"`
	Evidence         []string `json:"evidence"`
	SuggestedActions []string `json:"suggested_actions"`
	RiskLevel        string   `json:"risk_level"`
	NeedHumanConfirm bool     `json:"need_human_confirm"`
}

// GenerateDirectDiagnosis sends a live evidence snapshot directly to the model.
// The caller is responsible for ensuring the payload does not contain labels or
// golden answers.
func (c *OpenAICompatibleClient) GenerateDirectDiagnosis(ctx context.Context, snapshot interface{}) (*DirectDiagnosis, error) {
	if c == nil || c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return nil, fmt.Errorf("llm client is not configured")
	}
	start := time.Now()
	endpoint, err := c.chatCompletionsURL()
	if err != nil {
		return nil, err
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal direct diagnosis snapshot: %w", err)
	}
	payload := chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{
				Role: "system",
				Content: strings.Join([]string{
					"You are a Kubernetes root-cause diagnosis baseline.",
					"Diagnose only from the supplied live evidence. Do not invent absent facts.",
					"Return strict JSON with fields: fault_type, root_cause_summary, confidence_score, evidence, suggested_actions, risk_level, need_human_confirm.",
					"Evidence entries must quote or compactly restate concrete supplied observations.",
					"Suggested actions are advisory and must not contain destructive or mutating commands.",
					"Use fault_type=unknown and confidence_score<=0.4 when evidence is insufficient.",
				}, "\n"),
			},
			{Role: "user", Content: string(snapshotJSON)},
		},
		Temperature:    0.1,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}
	if err := applyContextTokenBudget(ctx, &payload); err != nil {
		return nil, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("direct llm request failed: %s: %s", resp.Status, string(respBody))
	}
	var raw chatCompletionResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, err
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("direct llm response error: %s: %s", raw.Error.Type, raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("direct llm response has no choices")
	}
	c.captureUsage(ctx, raw.Usage, time.Since(start))

	content := stripMarkdownFences(raw.Choices[0].Message.Content)
	var result DirectDiagnosis
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("parse direct llm response: %w", err)
	}
	result.ConfidenceScore = clampDirectConfidence(result.ConfidenceScore)
	if strings.TrimSpace(result.FaultType) == "" {
		result.FaultType = "unknown"
	}
	if strings.TrimSpace(result.RootCauseSummary) == "" {
		result.RootCauseSummary = "Insufficient evidence for a reliable root cause."
	}
	result.NeedHumanConfirm = true
	result.SuggestedActions = sanitizeDirectActions(result.SuggestedActions)
	return &result, nil
}

func clampDirectConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func sanitizeDirectActions(actions []string) []string {
	result := make([]string, 0, len(actions))
	for _, action := range actions {
		action = strings.TrimSpace(action)
		if action == "" {
			continue
		}
		blocked := false
		for _, pattern := range dangerousActionPatterns {
			if pattern.MatchString(action) {
				blocked = true
				break
			}
		}
		if !blocked {
			result = append(result, action)
		}
	}
	return result
}
