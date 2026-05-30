package prometheus

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
	"kubesage/internal/resilience"
)

const defaultQueryStep = 30 * time.Second

type Client struct {
	baseURL string
	timeout time.Duration
	http    *http.Client
	retry   resilience.RetryConfig
}

type Point struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

type Series struct {
	Metric map[string]string `json:"metric,omitempty"`
	Points []Point           `json:"points"`
}

type QueryRangeResult struct {
	Query       string    `json:"query"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	StepSeconds int64     `json:"step_seconds"`
	Series      []Series  `json:"series"`
	Warnings    []string  `json:"warnings,omitempty"`
}

// NewClient builds a Prometheus HTTP client from config.
// prometheus.base_url is preferred, while prometheus.address is kept as a
// compatibility fallback for older config files.
func NewClient(cfg config.PrometheusConfig) *Client {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(cfg.Address)
	}
	if baseURL != "" && !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	return &Client{
		baseURL: baseURL,
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
		retry: resilience.FromMilliseconds(
			cfg.RetryMaxAttempts,
			cfg.RetryInitialBackoffMS,
			cfg.RetryMaxBackoffMS,
		),
	}
}

// Configured reports whether the client has a usable Prometheus endpoint.
func (c *Client) Configured() bool {
	return c != nil && c.baseURL != ""
}

// QueryMemoryWorkingSetBytes queries container_memory_working_set_bytes for one
// Kubernetes container over a time range.
func (c *Client) QueryMemoryWorkingSetBytes(ctx context.Context, namespace, podName, containerName string, start, end time.Time) (*QueryRangeResult, error) {
	// kube-state/cAdvisor exporters commonly expose these Kubernetes labels.
	// If a cluster relabels them differently, this is the one query to adapt.
	query := fmt.Sprintf(`container_memory_working_set_bytes{namespace=%q,pod=%q,container=%q}`, namespace, podName, containerName)
	return c.QueryRange(ctx, query, start, end, defaultQueryStep)
}

// QueryMemoryUsage keeps the older memory query API and returns flattened
// points for callers that do not need series labels.
func (c *Client) QueryMemoryUsage(ctx context.Context, namespace, podName, containerName string, start, end time.Time) ([]Point, error) {
	result, err := c.QueryMemoryWorkingSetBytes(ctx, namespace, podName, containerName, start, end)
	if err != nil || result == nil {
		return nil, err
	}
	return result.AllPoints(), nil
}

// QueryCPUUsage queries a five-minute CPU usage rate for one container.
func (c *Client) QueryCPUUsage(ctx context.Context, namespace, podName, containerName string, start, end time.Time) ([]Point, error) {
	query := fmt.Sprintf(`rate(container_cpu_usage_seconds_total{namespace=%q,pod=%q,container=%q}[5m])`, namespace, podName, containerName)
	result, err := c.QueryRange(ctx, query, start, end, defaultQueryStep)
	if err != nil || result == nil {
		return nil, err
	}
	return result.AllPoints(), nil
}

// QueryRestartCount queries the kube-state-metrics restart counter for one
// container over a time range.
func (c *Client) QueryRestartCount(ctx context.Context, namespace, podName, containerName string, start, end time.Time) ([]Point, error) {
	query := fmt.Sprintf(`kube_pod_container_status_restarts_total{namespace=%q,pod=%q,container=%q}`, namespace, podName, containerName)
	result, err := c.QueryRange(ctx, query, start, end, defaultQueryStep)
	if err != nil || result == nil {
		return nil, err
	}
	return result.AllPoints(), nil
}

// QueryRange calls Prometheus /api/v1/query_range and returns the parsed matrix
// response with query metadata for evidence storage.
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*QueryRangeResult, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("prometheus base_url is not configured")
	}
	if step <= 0 {
		step = defaultQueryStep
	}
	if end.Before(start) {
		return nil, fmt.Errorf("prometheus query end time is before start time")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Use Prometheus' HTTP API directly so analyzers can store the exact query,
	// time window, warnings, series labels, and sample points as evidence.
	u, err := c.apiURL("/api/v1/query_range")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	q.Set("step", strconv.FormatInt(int64(step.Seconds()), 10))
	u.RawQuery = q.Encode()

	var result *QueryRangeResult
	err = resilience.Do(ctx, c.retry, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("prometheus query failed: %s", resp.Status)
		}

		var raw queryRangeResponse
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			return err
		}
		if raw.Status != "success" {
			if raw.Error != "" {
				return fmt.Errorf("prometheus query failed: %s: %s", raw.ErrorType, raw.Error)
			}
			return fmt.Errorf("prometheus query failed: status=%s", raw.Status)
		}
		result = raw.toResult(query, start, end, step)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// apiURL joins the configured base URL with a Prometheus API path.
func (c *Client) apiURL(apiPath string) (*url.URL, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, err
	}
	u.Path = path.Join(u.Path, apiPath)
	return u, nil
}

// AllPoints flattens all time series points into a single slice.
func (r *QueryRangeResult) AllPoints() []Point {
	if r == nil {
		return nil
	}
	points := make([]Point, 0)
	for _, series := range r.Series {
		points = append(points, series.Points...)
	}
	return points
}

type queryRangeResponse struct {
	Status    string   `json:"status"`
	ErrorType string   `json:"errorType,omitempty"`
	Error     string   `json:"error,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
	Data      struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// toResult converts the Prometheus matrix response into KubeSage's internal
// result shape. It keeps series boundaries so callers can inspect labels.
func (r queryRangeResponse) toResult(query string, start, end time.Time, step time.Duration) *QueryRangeResult {
	result := &QueryRangeResult{
		Query:       query,
		Start:       start,
		End:         end,
		StepSeconds: int64(step.Seconds()),
		Series:      make([]Series, 0, len(r.Data.Result)),
		Warnings:    r.Warnings,
	}
	for _, rawSeries := range r.Data.Result {
		series := Series{
			Metric: rawSeries.Metric,
			Points: make([]Point, 0, len(rawSeries.Values)),
		}
		for _, value := range rawSeries.Values {
			point, ok := parsePoint(value)
			if ok {
				series.Points = append(series.Points, point)
			}
		}
		result.Series = append(result.Series, series)
	}
	return result
}

// parsePoint converts one Prometheus [timestamp, value] pair into a typed point.
func parsePoint(value []interface{}) (Point, bool) {
	if len(value) != 2 {
		return Point{}, false
	}
	ts, ok := value[0].(float64)
	if !ok {
		return Point{}, false
	}
	var metricValue float64
	switch typed := value[1].(type) {
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return Point{}, false
		}
		metricValue = parsed
	case float64:
		metricValue = typed
	default:
		return Point{}, false
	}
	return Point{Timestamp: time.Unix(int64(ts), 0), Value: metricValue}, true
}
