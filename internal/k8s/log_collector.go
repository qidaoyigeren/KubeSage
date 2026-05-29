package k8s

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"kubesage/internal/diagnostic"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LogCollector struct {
	client *Client
}

type LogCollectionOptions struct {
	ContainerName string
	FaultTime     time.Time
	WindowBefore  time.Duration
	WindowAfter   time.Duration
}

// NewLogCollector creates a Kubernetes pod log collector.
func NewLogCollector(client *Client) *LogCollector {
	return &LogCollector{client: client}
}

// CollectPodLogs collects current and previous logs for the requested
// container, or for all pod containers when ContainerName is empty.
func (c *LogCollector) CollectPodLogs(ctx context.Context, pod *corev1.Pod, opts LogCollectionOptions) ([]diagnostic.ContainerLogs, error) {
	if c.client == nil || c.client.Clientset == nil {
		return nil, errors.New("kubernetes client is not initialized")
	}
	if pod == nil {
		return nil, errors.New("pod is nil")
	}
	names := []string{}
	if opts.ContainerName != "" {
		names = append(names, opts.ContainerName)
	} else {
		for _, container := range pod.Spec.Containers {
			names = append(names, container.Name)
		}
	}
	windowStart, windowEnd, precise, fallback := logWindow(opts)

	result := make([]diagnostic.ContainerLogs, 0, len(names))
	for _, name := range names {
		// Current and previous logs are best-effort; one failed stream should not
		// prevent the rest of the diagnostic context from being collected.
		current, _ := c.getLogs(ctx, pod.Namespace, pod.Name, name, false, windowStart, windowEnd, precise)
		previous, _ := c.getLogs(ctx, pod.Namespace, pod.Name, name, true, windowStart, windowEnd, precise)
		result = append(result, diagnostic.ContainerLogs{
			ContainerName: name,
			Current:       current,
			Previous:      previous,
			FaultTime:     opts.FaultTime,
			WindowStart:   windowStart,
			WindowEnd:     windowEnd,
			Precise:       precise,
			Fallback:      fallback,
		})
	}
	return result, nil
}

// getLogs reads current or previous logs for one pod container. When precise is
// true it uses SinceTime and locally trims anything after the desired window.
func (c *LogCollector) getLogs(ctx context.Context, namespace, podName, container string, previous bool, windowStart, windowEnd time.Time, precise bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.client.Timeout)
	defer cancel()
	tail := c.client.TailLines
	options := &corev1.PodLogOptions{
		Container: container,
		Previous:  previous,
		TailLines: &tail,
	}
	if precise {
		since := metav1.NewTime(windowStart)
		options.SinceTime = &since
		options.Timestamps = true
		options.TailLines = nil
	}
	req := c.client.Clientset.CoreV1().Pods(namespace).GetLogs(podName, options)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	bytes, err := io.ReadAll(stream)
	if err != nil {
		return "", err
	}
	text := string(bytes)
	if precise {
		return filterTimestampedLogWindow(text, windowStart, windowEnd), nil
	}
	return text, nil
}

// logWindow converts fault time and before/after durations into a log window.
func logWindow(opts LogCollectionOptions) (time.Time, time.Time, bool, string) {
	if opts.FaultTime.IsZero() {
		return time.Time{}, time.Time{}, false, "fault_time unavailable; collected latest logs"
	}
	before := opts.WindowBefore
	if before <= 0 {
		before = 120 * time.Second
	}
	after := opts.WindowAfter
	if after <= 0 {
		after = 60 * time.Second
	}
	return opts.FaultTime.Add(-before), opts.FaultTime.Add(after), true, ""
}

// filterTimestampedLogWindow keeps timestamped Kubernetes log lines inside the
// requested window and preserves unparsable lines as context.
func filterTimestampedLogWindow(text string, start, end time.Time) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		ts, ok := parseKubernetesLogTimestamp(line)
		if !ok || (!ts.Before(start) && !ts.After(end)) {
			filtered = append(filtered, line)
		}
	}
	return strings.Join(filtered, "\n")
}

// parseKubernetesLogTimestamp parses the RFC3339 timestamp prefix emitted when
// PodLogOptions.Timestamps is enabled.
func parseKubernetesLogTimestamp(line string) (time.Time, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, fields[0])
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}
