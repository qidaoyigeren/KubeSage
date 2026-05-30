package loki

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"kubesage/internal/config"
)

type Client struct {
	baseURL  string
	tenantID string
	timeout  time.Duration
	http     *http.Client
}

type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Line      string    `json:"line"`
}

type queryRangeResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
	ErrorType string `json:"errorType,omitempty"`
	Error     string `json:"error,omitempty"`
}

// NewClient creates a Loki HTTP client when loki.enabled/base_url are set.
func NewClient(cfg config.LokiConfig) *Client {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(cfg.Address)
	}
	if baseURL != "" && !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		tenantID: strings.TrimSpace(cfg.TenantID),
		timeout:  timeout,
		http:     &http.Client{Timeout: timeout},
	}
}

// Configured reports whether Loki is available for queries.
func (c *Client) Configured() bool {
	return c != nil && c.baseURL != ""
}

// QueryPodLogs fetches Loki log lines for one Kubernetes pod/container window.
func (c *Client) QueryPodLogs(ctx context.Context, namespace, podName, containerName string, start, end time.Time) ([]LogEntry, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("loki base_url is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	query := fmt.Sprintf(`{namespace=%q,pod=%q}`, namespace, podName)
	if containerName != "" {
		query = fmt.Sprintf(`{namespace=%q,pod=%q,container=%q}`, namespace, podName, containerName)
	}
	u, err := c.apiURL("/loki/api/v1/query_range")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("start", strconv.FormatInt(start.UnixNano(), 10))
	q.Set("end", strconv.FormatInt(end.UnixNano(), 10))
	q.Set("limit", "200")
	q.Set("direction", "forward")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.tenantID != "" {
		req.Header.Set("X-Scope-OrgID", c.tenantID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("loki query failed: %s", resp.Status)
	}
	var raw queryRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	if raw.Status != "success" {
		return nil, fmt.Errorf("loki query failed: %s: %s", raw.ErrorType, raw.Error)
	}
	return flattenEntries(raw), nil
}

// apiURL joins the configured base URL with a Loki API path.
func (c *Client) apiURL(apiPath string) (*url.URL, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, apiPath)
	return u, nil
}

// flattenEntries converts Loki stream values into timestamped log entries.
func flattenEntries(raw queryRangeResponse) []LogEntry {
	entries := []LogEntry{}
	for _, stream := range raw.Data.Result {
		for _, value := range stream.Values {
			if len(value) < 2 {
				continue
			}
			ns, err := strconv.ParseInt(value[0], 10, 64)
			if err != nil {
				continue
			}
			entries = append(entries, LogEntry{
				Timestamp: time.Unix(0, ns),
				Line:      value[1],
			})
		}
	}
	return entries
}

// FormatEntries converts Loki entries into log text used by analyzers.
func FormatEntries(entries []LogEntry) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		lines = append(lines, entry.Timestamp.Format(time.RFC3339Nano)+" "+entry.Line)
	}
	return strings.Join(lines, "\n")
}
