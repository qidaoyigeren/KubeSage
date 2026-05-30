package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"kubesage/internal/diagnostic"
	promapi "kubesage/internal/prometheus"

	corev1 "k8s.io/api/core/v1"
)

var metricTrendWindows = []time.Duration{time.Hour, 6 * time.Hour}

func (s *SnapshotService) collectMetricTrends(ctx context.Context, diagCtx *diagnostic.DiagnosticContext, req PodDiagnosisRequest) {
	if s == nil || s.prometheus == nil || !s.prometheus.Configured() || diagCtx == nil || diagCtx.Pod == nil || !req.IncludeMetrics {
		return
	}
	end := diagCtx.FaultTime
	if end.IsZero() {
		end = time.Now()
	}
	containers := selectedContainers(diagCtx.Pod.Spec.Containers, req.ContainerName)
	for _, container := range containers {
		for _, profile := range metricProfiles(diagCtx.Namespace, diagCtx.PodName, container.Name) {
			for _, window := range metricTrendWindows {
				start := end.Add(-window)
				trend := diagnostic.MetricTrend{
					Profile:     profile.profile,
					Metric:      profile.metric,
					Window:      window.String(),
					Query:       profile.query,
					WindowStart: start,
					WindowEnd:   end,
				}
				result, err := s.prometheus.QueryRange(ctx, profile.query, start, end, time.Minute)
				if err != nil {
					trend.Classification = "query_failed"
					trend.Error = err.Error()
					diagCtx.MetricTrends = append(diagCtx.MetricTrends, trend)
					continue
				}
				points := result.AllPoints()
				trend = classifyTrend(trend, points)
				diagCtx.MetricTrends = append(diagCtx.MetricTrends, trend)
			}
		}
	}
}

type metricProfile struct {
	profile string
	metric  string
	query   string
}

func metricProfiles(namespace, podName, containerName string) []metricProfile {
	selector := fmt.Sprintf(`namespace=%q,pod=%q,container=%q`, namespace, podName, containerName)
	return []metricProfile{
		{
			profile: "oom_memory_growth",
			metric:  "container_memory_working_set_bytes",
			query:   fmt.Sprintf(`container_memory_working_set_bytes{%s}`, selector),
		},
		{
			profile: "traffic_or_cpu_spike",
			metric:  "container_cpu_usage_seconds_total",
			query:   fmt.Sprintf(`rate(container_cpu_usage_seconds_total{%s}[5m])`, selector),
		},
		{
			profile: "restart_trend",
			metric:  "kube_pod_container_status_restarts_total",
			query:   fmt.Sprintf(`kube_pod_container_status_restarts_total{%s}`, selector),
		},
	}
}

func selectedContainers(containers []corev1.Container, name string) []corev1.Container {
	if name == "" {
		return containers
	}
	for _, container := range containers {
		if container.Name == name {
			return []corev1.Container{container}
		}
	}
	return containers
}

func classifyTrend(trend diagnostic.MetricTrend, points []promapi.Point) diagnostic.MetricTrend {
	trend.SampleCount = len(points)
	if len(points) == 0 {
		trend.Classification = "no_data"
		return trend
	}
	trend.MinValue = math.MaxFloat64
	var sum float64
	for i, point := range points {
		if i == 0 {
			trend.FirstValue = point.Value
		}
		trend.LastValue = point.Value
		if point.Value < trend.MinValue {
			trend.MinValue = point.Value
		}
		if point.Value > trend.MaxValue {
			trend.MaxValue = point.Value
		}
		sum += point.Value
	}
	trend.AvgValue = sum / float64(len(points))
	if trend.FirstValue > 0 {
		trend.GrowthRatio = (trend.LastValue - trend.FirstValue) / trend.FirstValue
	}
	switch {
	case len(points) >= 4 && trend.GrowthRatio >= 0.5:
		trend.Classification = "progressive_growth"
	case len(points) >= 4 && trend.AvgValue > 0 && trend.MaxValue >= trend.AvgValue*2.5:
		trend.Classification = "sudden_spike"
	case trend.Profile == "restart_trend" && trend.LastValue > trend.FirstValue:
		trend.Classification = "restart_increasing"
	default:
		trend.Classification = "stable"
	}
	return trend
}
