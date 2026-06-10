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
	"sync"
	"time"

	"kubesage/internal/agent"
	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
)

type OpenAICompatibleClient struct {
	baseURL string
	apiKey  string
	model   string
	http    *http.Client
	mu      sync.Mutex
	usage   UsageRecord
	total   UsageRecord
	calls   int
}

type chatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
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
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
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
	start := time.Now()
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
	c.captureUsage(ctx, raw.Usage, time.Since(start))
	summary, err := parseEnhancedSummary(raw.Choices[0].Message.Content)
	if err != nil {
		return nil, err
	}
	sanitizeEnhancedSummary(summary)
	return summary, nil
}

func (c *OpenAICompatibleClient) GenerateGroundedSummary(ctx context.Context, prompt GroundedPrompt) (*GroundedSummary, error) {
	if c == nil || c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return nil, fmt.Errorf("llm client is not configured")
	}
	start := time.Now()
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
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm grounded request failed: %s: %s", resp.Status, string(respBody))
	}
	var raw chatCompletionResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, err
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("llm grounded response error: %s: %s", raw.Error.Type, raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("llm grounded response has no choices")
	}
	c.captureUsage(ctx, raw.Usage, time.Since(start))
	return parseGroundedSummary(raw.Choices[0].Message.Content)
}

func (c *OpenAICompatibleClient) LastUsage() UsageRecord {
	if c == nil {
		return UsageRecord{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}

func (c *OpenAICompatibleClient) CumulativeUsage() UsageRecord {
	if c == nil {
		return UsageRecord{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total
}

func (c *OpenAICompatibleClient) UsageCallCount() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *OpenAICompatibleClient) CumulativeTokenUsage() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.total.TotalTokens
}

func (c *OpenAICompatibleClient) GenerateAgentPlan(ctx context.Context, prompt agent.PlanPrompt) (agent.Plan, error) {
	if c == nil || c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return agent.Plan{}, fmt.Errorf("llm client is not configured")
	}
	start := time.Now()
	endpoint, err := c.chatCompletionsURL()
	if err != nil {
		return agent.Plan{}, err
	}
	payloadBytes, _ := json.MarshalIndent(prompt, "", "  ")
	payload := chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: PlanSystemPrompt(StageEarly, prompt.Goal.ExpectedFault)},
			{Role: "user", Content: string(payloadBytes)},
		},
		Temperature:    0.1,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}
	if err := applyContextTokenBudget(ctx, &payload); err != nil {
		return agent.Plan{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return agent.Plan{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return agent.Plan{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return agent.Plan{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return agent.Plan{}, err
	}
	if resp.StatusCode >= 300 {
		return agent.Plan{}, fmt.Errorf("llm plan request failed: %s: %s", resp.Status, string(respBody))
	}
	var raw chatCompletionResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return agent.Plan{}, err
	}
	if raw.Error != nil {
		return agent.Plan{}, fmt.Errorf("llm plan response error: %s: %s", raw.Error.Type, raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return agent.Plan{}, fmt.Errorf("llm plan response has no choices")
	}
	c.captureUsage(ctx, raw.Usage, time.Since(start))
	content := stripMarkdownFences(raw.Choices[0].Message.Content)
	plan, err := parseFlexiblePlan(content, prompt.Goal)
	if err != nil {
		return agent.Plan{}, fmt.Errorf("llm plan parse error: %w; raw=%s", err, truncateString(content, 500))
	}
	if len(plan.Steps) == 0 {
		return agent.Plan{}, fmt.Errorf("llm plan has no steps")
	}
	return plan, nil
}

// GeneratePlanAdjustment asks the LLM to revise the current plan based on
// observations and hypothesis scores collected so far.
func (c *OpenAICompatibleClient) GeneratePlanAdjustment(ctx context.Context, prompt agent.AdjustmentPrompt) (agent.Plan, error) {
	if c == nil || c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return agent.Plan{}, fmt.Errorf("llm client is not configured")
	}
	start := time.Now()
	endpoint, err := c.chatCompletionsURL()
	if err != nil {
		return agent.Plan{}, err
	}
	payloadBytes, _ := json.MarshalIndent(prompt, "", "  ")
	payload := chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: AdjustmentSystemPrompt(DetermineStage(len(prompt.CurrentPlan.Steps), 12), prompt.Goal.ExpectedFault)},
			{Role: "user", Content: string(payloadBytes)},
		},
		Temperature:    0.1,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}
	if err := applyContextTokenBudget(ctx, &payload); err != nil {
		return agent.Plan{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return agent.Plan{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return agent.Plan{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return agent.Plan{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return agent.Plan{}, err
	}
	if resp.StatusCode >= 300 {
		return agent.Plan{}, fmt.Errorf("llm adjustment request failed: %s: %s", resp.Status, string(respBody))
	}
	var raw chatCompletionResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return agent.Plan{}, err
	}
	if raw.Error != nil {
		return agent.Plan{}, fmt.Errorf("llm adjustment response error: %s: %s", raw.Error.Type, raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return agent.Plan{}, fmt.Errorf("llm adjustment response has no choices")
	}
	c.captureUsage(ctx, raw.Usage, time.Since(start))
	content := stripMarkdownFences(raw.Choices[0].Message.Content)
	plan, err := parseFlexiblePlan(content, prompt.Goal)
	if err != nil {
		return agent.Plan{}, fmt.Errorf("llm adjustment parse error: %w; raw=%s", err, truncateString(content, 500))
	}
	return plan, nil
}

// ScoreHypotheses asks the LLM to re-rank hypothesis candidates based on the
// evidence. Returns adjusted scores with the same hypothesis types.
func (c *OpenAICompatibleClient) ScoreHypotheses(ctx context.Context, candidates []agent.HypothesisScore) ([]agent.HypothesisScore, error) {
	if c == nil || c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return nil, fmt.Errorf("llm client is not configured")
	}
	start := time.Now()
	endpoint, err := c.chatCompletionsURL()
	if err != nil {
		return nil, err
	}

	payloadBytes, _ := json.MarshalIndent(map[string]interface{}{
		"candidates": candidates,
	}, "", "  ")
	payload := chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: strings.Join([]string{
				"You are KubeSage's hypothesis scoring engine.",
				"Given hypothesis candidates with evidence-derived confidence scores,",
				"identify semantic conflicts that justify lowering confidence.",
				"Do not raise confidence, invent evidence, or treat a keyword mention as proof of root cause.",
				"Return JSON: {\"hypotheses\": [{\"type\": string, \"confidence\": float, \"summary\": string}]}",
				"Only include hypotheses you want to adjust. Keep type names exactly as provided.",
				"Confidence must be between 0 and 1.",
			}, "\n")},
			{Role: "user", Content: string(payloadBytes)},
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
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm hypothesis scoring failed: %s: %s", resp.Status, string(respBody))
	}
	var raw chatCompletionResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, err
	}
	if raw.Error != nil {
		return nil, fmt.Errorf("llm hypothesis scoring error: %s: %s", raw.Error.Type, raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return nil, fmt.Errorf("llm hypothesis scoring has no choices")
	}
	c.captureUsage(ctx, raw.Usage, time.Since(start))

	content := strings.TrimSpace(raw.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	var parsed struct {
		Hypotheses []struct {
			Type       string  `json:"type"`
			Confidence float64 `json:"confidence"`
			Summary    string  `json:"summary"`
		} `json:"hypotheses"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &parsed); err != nil {
		return nil, err
	}
	result := make([]agent.HypothesisScore, 0, len(parsed.Hypotheses))
	for _, h := range parsed.Hypotheses {
		if h.Confidence < 0 {
			h.Confidence = 0
		}
		if h.Confidence > 1 {
			h.Confidence = 1
		}
		result = append(result, agent.HypothesisScore{
			Type:       h.Type,
			Confidence: h.Confidence,
			Summary:    h.Summary,
		})
	}
	return result, nil
}

// GenerateReflection asks the LLM to decide whether to continue investigating
// or stop, based on the current evidence and hypothesis scores.
func (c *OpenAICompatibleClient) GenerateReflection(ctx context.Context, prompt agent.ReflectionPrompt) (agent.ReflectionResult, error) {
	if c == nil || c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return agent.ReflectionResult{}, fmt.Errorf("llm client is not configured")
	}
	start := time.Now()
	endpoint, err := c.chatCompletionsURL()
	if err != nil {
		return agent.ReflectionResult{}, err
	}
	payloadBytes, _ := json.MarshalIndent(prompt, "", "  ")
	payload := chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: ReflectionSystemPrompt(DetermineStage(len(prompt.Plan.Steps), 12), prompt.Goal.ExpectedFault)},
			{Role: "user", Content: string(payloadBytes)},
		},
		Temperature:    0.1,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}
	if err := applyContextTokenBudget(ctx, &payload); err != nil {
		return agent.ReflectionResult{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return agent.ReflectionResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return agent.ReflectionResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return agent.ReflectionResult{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return agent.ReflectionResult{}, err
	}
	if resp.StatusCode >= 300 {
		return agent.ReflectionResult{}, fmt.Errorf("llm reflection request failed: %s: %s", resp.Status, string(respBody))
	}
	var raw chatCompletionResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return agent.ReflectionResult{}, err
	}
	if raw.Error != nil {
		return agent.ReflectionResult{}, fmt.Errorf("llm reflection response error: %s: %s", raw.Error.Type, raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return agent.ReflectionResult{}, fmt.Errorf("llm reflection response has no choices")
	}
	c.captureUsage(ctx, raw.Usage, time.Since(start))
	content := strings.TrimSpace(raw.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	var result agent.ReflectionResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &result); err != nil {
		return agent.ReflectionResult{}, err
	}
	return result, nil
}

// SummarizeEvidences uses a fast LLM call to produce a concise summary of
// evidence records that were not included in the detailed prompt. The prompt
// is deliberately minimal to keep latency and token cost low.
func (c *OpenAICompatibleClient) SummarizeEvidences(ctx context.Context, records []diagnostic.EvidenceRecord) (string, error) {
	if c == nil || c.baseURL == "" || c.apiKey == "" || c.model == "" {
		return "", fmt.Errorf("llm client is not configured")
	}
	if len(records) == 0 {
		return "", nil
	}

	// Build a compact evidence list for the summarizer.
	var b strings.Builder
	for i, r := range records {
		if i >= 60 {
			break
		}
		content := r.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		b.WriteString(fmt.Sprintf("[%s|%s] %s: %s\n", r.Severity, r.SourceType, r.Title, content))
	}

	start := time.Now()
	endpoint, err := c.chatCompletionsURL()
	if err != nil {
		return "", err
	}
	payload := chatCompletionRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: evidenceSummarizerSystemPrompt},
			{Role: "user", Content: b.String()},
		},
		Temperature: 0.1,
		MaxTokens:   300,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("evidence summarizer failed: %s: %s", resp.Status, string(respBody))
	}
	var raw chatCompletionResponse
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return "", err
	}
	if raw.Error != nil {
		return "", fmt.Errorf("evidence summarizer error: %s: %s", raw.Error.Type, raw.Error.Message)
	}
	if len(raw.Choices) == 0 {
		return "", fmt.Errorf("evidence summarizer returned no choices")
	}
	c.captureUsage(ctx, raw.Usage, time.Since(start))
	return strings.TrimSpace(raw.Choices[0].Message.Content), nil
}

var evidenceSummarizerSystemPrompt = strings.Join([]string{
	"You are a Kubernetes diagnosis evidence summarizer.",
	"Given a list of diagnostic evidence records that were NOT included in the main diagnosis prompt, produce a concise 2-4 sentence summary.",
	"Focus on: what types of evidence exist, any patterns or anomalies, and anything that could affect the diagnosis.",
	"Do NOT repeat individual evidence items verbatim. Synthesize and compress.",
	"Do NOT invent facts not present in the evidence. Do NOT suggest actions.",
	"Return plain text only, no JSON, no markdown.",
}, "\n")

// Ensure OpenAICompatibleClient implements the agent interfaces at compile time.
var _ agent.PlanClient = (*OpenAICompatibleClient)(nil)
var _ agent.TokenUsageReporter = (*OpenAICompatibleClient)(nil)

func (c *OpenAICompatibleClient) captureUsage(ctx context.Context, usage *struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}, latency time.Duration) {
	if c == nil {
		return
	}
	record := UsageRecord{
		Provider:  "openai_compatible",
		Model:     c.model,
		LatencyMS: latency.Milliseconds(),
	}
	if usage != nil {
		record.PromptTokens = usage.PromptTokens
		record.CompletionTokens = usage.CompletionTokens
		record.TotalTokens = usage.TotalTokens
	}
	c.mu.Lock()
	c.usage = record
	if c.total.Provider == "" {
		c.total.Provider = record.Provider
		c.total.Model = record.Model
	}
	c.total.PromptTokens += record.PromptTokens
	c.total.CompletionTokens += record.CompletionTokens
	c.total.TotalTokens += record.TotalTokens
	c.total.LatencyMS += record.LatencyMS
	c.total.EstimatedCost += record.EstimatedCost
	c.calls++
	c.mu.Unlock()
	agent.RecordLLMTokenUsage(ctx, record.TotalTokens)
}

func applyContextTokenBudget(ctx context.Context, payload *chatCompletionRequest) error {
	if payload == nil {
		return fmt.Errorf("llm payload is nil")
	}
	remaining, maxCompletion, limited := agent.RemainingLLMTokens(ctx)
	if !limited {
		return nil
	}
	estimatedPromptTokens := estimateMessageTokens(payload.Messages)
	availableCompletion := remaining - estimatedPromptTokens
	if availableCompletion <= 0 {
		// Budget exhausted — allow the request to proceed without setting
		// MaxTokens. The model's own context window is the real hard limit.
		// Token usage is tracked for observability but no longer blocks calls.
		return nil
	}
	if maxCompletion > 0 && availableCompletion > maxCompletion {
		availableCompletion = maxCompletion
	}
	payload.MaxTokens = availableCompletion
	return nil
}

func estimateMessageTokens(messages []chatMessage) int {
	estimated := 8
	for _, message := range messages {
		// Three UTF-8 bytes per token is deliberately conservative for mixed
		// English/Chinese operational prompts.
		estimated += 4 + agent.EstimateTokens(message.Content)
	}
	return estimated
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

func parseGroundedSummary(content string) (*GroundedSummary, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var summary GroundedSummary
	if err := json.Unmarshal([]byte(content), &summary); err != nil {
		return nil, err
	}
	if summary.RootCauseConfirmation.AgreementLevel == "" {
		return nil, fmt.Errorf("llm response missing root_cause_confirmation.agreement_level")
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

// stripMarkdownFences removes ```json ... ``` wrappers from LLM output.
func stripMarkdownFences(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// truncateString truncates s to maxLen runes, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// flexibleStep is an intermediate struct that accepts both LLM-generated field
// names ("tool"/"params") and canonical names ("tool_name"/"input").
type flexibleStep struct {
	ID            string                 `json:"id"`
	ToolName      string                 `json:"tool_name"`
	ToolNameAlt   string                 `json:"tool"`
	Input         map[string]interface{} `json:"input"`
	InputAlt      map[string]interface{} `json:"params"`
	Reason        string                 `json:"reason"`
	ReasonAlt     string                 `json:"description"`
	Critical      bool                   `json:"critical"`
	ParallelGroup string                 `json:"parallel_group"`
}

// flexiblePlan is an intermediate struct that accepts stop_condition as either
// a string or an array of strings.
type flexiblePlan struct {
	Summary              string          `json:"plan_summary"`
	Steps                []flexibleStep  `json:"steps"`
	ExpectedObservations []string        `json:"expected_observations"`
	StopCondition        json.RawMessage `json:"stop_condition"`
}

// parseFlexiblePlan parses LLM-generated JSON into an agent.Plan, tolerating
// common field name variations and type mismatches.
func parseFlexiblePlan(content string, goal agent.Goal) (agent.Plan, error) {
	var fp flexiblePlan
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &fp); err != nil {
		return agent.Plan{}, err
	}

	// Parse stop_condition: may be string or []string.
	var stopConditions []string
	if len(fp.StopCondition) > 0 {
		// Try as array first.
		if err := json.Unmarshal(fp.StopCondition, &stopConditions); err != nil {
			// Try as single string.
			var single string
			if err2 := json.Unmarshal(fp.StopCondition, &single); err2 == nil {
				stopConditions = []string{single}
			}
		}
	}

	// Map flexible steps to canonical steps.
	steps := make([]agent.PlanStep, 0, len(fp.Steps))
	for i, fs := range fp.Steps {
		toolName := fs.ToolName
		if toolName == "" {
			toolName = fs.ToolNameAlt
		}
		input := fs.Input
		if len(input) == 0 {
			input = fs.InputAlt
		}
		reason := fs.Reason
		if reason == "" {
			reason = fs.ReasonAlt
		}
		if toolName == "" {
			continue
		}
		// Fill default input from goal if empty.
		if len(input) == 0 {
			input = map[string]interface{}{
				"namespace": goal.Namespace,
				"pod_name":  goal.PodName,
			}
		}
		id := fs.ID
		if id == "" {
			id = fmt.Sprintf("step-%02d", i+1)
		}
		steps = append(steps, agent.PlanStep{
			ID:            id,
			ToolName:      toolName,
			Input:         input,
			Reason:        reason,
			Critical:      fs.Critical,
			ParallelGroup: fs.ParallelGroup,
		})
	}

	plan := agent.Plan{
		Summary:              fp.Summary,
		Steps:                steps,
		ExpectedObservations: fp.ExpectedObservations,
		StopCondition:        stopConditions,
	}
	return plan, nil
}
