package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"kubesage/internal/config"
)

type OpenAICompatibleClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
}

type chatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

var dangerousActionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bkubectl\s+delete\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+apply\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+patch\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+replace\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+scale\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+delete\s+(ns|namespace|namespaces)\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+delete\s+(pvc|persistentvolumeclaim|persistentvolumeclaims)\b`),
	regexp.MustCompile(`(?i)\bdrop\s+database\b`),
	regexp.MustCompile(`(?i)\bdelete\s+from\b`),
	regexp.MustCompile(`(?i)\btruncate\s+table\b`),
	regexp.MustCompile(`(?i)\brm\s+-rf\b`),
}

// NewOpenAICompatibleClient creates a DeepSeek/OpenAI-compatible chat client.
func NewOpenAICompatibleClient(cfg config.LLMConfig) LLMClient {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL != "" && !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	return &OpenAICompatibleClient{
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(cfg.APIKey),
		model:   strings.TrimSpace(cfg.Model),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// GenerateDiagnosisSummary sends a structured diagnosis prompt and parses the
// JSON response into an enhanced summary.
func (c *OpenAICompatibleClient) GenerateDiagnosisSummary(ctx context.Context, prompt Prompt) (*EnhancedSummary, error) {
	if c == nil || c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return nil, fmt.Errorf("llm client is not configured")
	}
	endpoint, err := c.chatCompletionsURL()
	if err != nil {
		return nil, err
	}
	payload := chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: prompt.System},
			{Role: "user", Content: prompt.User},
		},
		Temperature:    0.2,
		ResponseFormat: &responseFormat{Type: "json_object"},
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
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm request failed: %s: %s", resp.Status, string(respBody))
	}
	var raw chatCompletionResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, err
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("llm response error: %s: %s", raw.Error.Type, raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("llm response has no choices")
	}
	summary, err := parseEnhancedSummary(raw.Choices[0].Message.Content)
	if err != nil {
		return nil, err
	}
	sanitizeEnhancedSummary(summary)
	return summary, nil
}

// chatCompletionsURL resolves the OpenAI-compatible chat completions endpoint.
func (c *OpenAICompatibleClient) chatCompletionsURL() (string, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(u.Path, "/chat/completions")
	return u.String(), nil
}

// parseEnhancedSummary accepts raw JSON or fenced JSON from the model.
func parseEnhancedSummary(content string) (*EnhancedSummary, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var summary EnhancedSummary
	if err := json.Unmarshal([]byte(content), &summary); err != nil {
		return nil, err
	}
	if summary.RootCauseSummary == "" {
		return nil, fmt.Errorf("llm response missing root_cause_summary")
	}
	return &summary, nil
}

// sanitizeEnhancedSummary removes risky imperative command suggestions from the
// LLM output. KubeSage never executes these actions automatically.
func sanitizeEnhancedSummary(summary *EnhancedSummary) {
	if summary == nil {
		return
	}
	if summary.ConfidenceScore < 0 {
		summary.ConfidenceScore = 0
	}
	if summary.ConfidenceScore > 1 {
		summary.ConfidenceScore = 1
	}
	safeActions := make([]string, 0, len(summary.SuggestedActions))
	for _, action := range summary.SuggestedActions {
		if strings.TrimSpace(action) == "" || isDangerousAction(action) {
			continue
		}
		safeActions = append(safeActions, action)
	}
	if len(safeActions) == 0 {
		safeActions = append(safeActions, "Review the evidence chain and Runbook manually before making any high-risk change.")
	}
	summary.SuggestedActions = safeActions
	switch strings.ToLower(strings.TrimSpace(summary.RiskLevel)) {
	case "low", "medium", "high":
		summary.RiskLevel = strings.ToLower(strings.TrimSpace(summary.RiskLevel))
	default:
		summary.RiskLevel = "medium"
	}
	summary.NeedHumanConfirm = true
}

// isDangerousAction detects command-like suggestions that should never be
// surfaced as direct automatic actions.
func isDangerousAction(action string) bool {
	for _, pattern := range dangerousActionPatterns {
		if pattern.MatchString(action) {
			return true
		}
	}
	return false
}
