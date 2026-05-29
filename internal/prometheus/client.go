package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"kubesage/internal/config"
)

type Client struct {
	address string
	timeout time.Duration
	http    *http.Client
}

type Point struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

func NewClient(cfg config.PrometheusConfig) *Client {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		address: cfg.Address,
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) QueryMemoryUsage(ctx context.Context, namespace, podName, containerName string, start, end time.Time) ([]Point, error) {
	query := fmt.Sprintf(`container_memory_working_set_bytes{namespace=%q,pod=%q,container=%q}`, namespace, podName, containerName)
	return c.queryRange(ctx, query, start, end)
}

func (c *Client) QueryCPUUsage(ctx context.Context, namespace, podName, containerName string, start, end time.Time) ([]Point, error) {
	query := fmt.Sprintf(`rate(container_cpu_usage_seconds_total{namespace=%q,pod=%q,container=%q}[5m])`, namespace, podName, containerName)
	return c.queryRange(ctx, query, start, end)
}

func (c *Client) QueryRestartCount(ctx context.Context, namespace, podName, containerName string, start, end time.Time) ([]Point, error) {
	query := fmt.Sprintf(`kube_pod_container_status_restarts_total{namespace=%q,pod=%q,container=%q}`, namespace, podName, containerName)
	return c.queryRange(ctx, query, start, end)
}

func (c *Client) queryRange(ctx context.Context, query string, start, end time.Time) ([]Point, error) {
	if c == nil || c.address == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	u, err := url.Parse(c.address + "/api/v1/query_range")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("start", fmt.Sprintf("%d", start.Unix()))
	q.Set("end", fmt.Sprintf("%d", end.Unix()))
	q.Set("step", "30")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prometheus query failed: %s", resp.Status)
	}

	var raw struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Values [][]interface{} `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	points := []Point{}
	for _, series := range raw.Data.Result {
		for _, value := range series.Values {
			if len(value) != 2 {
				continue
			}
			ts, ok := value[0].(float64)
			if !ok {
				continue
			}
			var v float64
			switch typed := value[1].(type) {
			case string:
				_, _ = fmt.Sscanf(typed, "%f", &v)
			case float64:
				v = typed
			}
			points = append(points, Point{Timestamp: time.Unix(int64(ts), 0), Value: v})
		}
	}
	return points, nil
}
