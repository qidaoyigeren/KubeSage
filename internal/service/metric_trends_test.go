package service

import (
	"testing"
	"time"

	"kubesage/internal/diagnostic"
	promapi "kubesage/internal/prometheus"
)

func TestClassifyTrendProgressiveGrowth(t *testing.T) {
	trend := classifyTrend(diagnostic.MetricTrend{Profile: "oom_memory_growth"}, []promapi.Point{
		{Timestamp: time.Now(), Value: 100},
		{Timestamp: time.Now(), Value: 120},
		{Timestamp: time.Now(), Value: 150},
		{Timestamp: time.Now(), Value: 180},
	})
	if trend.Classification != "progressive_growth" {
		t.Fatalf("unexpected classification: %+v", trend)
	}
	if trend.SlopePerMinute <= 0 {
		t.Fatalf("expected positive slope: %+v", trend)
	}
}

func TestClassifyTrendSuddenSpike(t *testing.T) {
	trend := classifyTrend(diagnostic.MetricTrend{Profile: "traffic_or_cpu_spike"}, []promapi.Point{
		{Timestamp: time.Now(), Value: 10},
		{Timestamp: time.Now(), Value: 10},
		{Timestamp: time.Now(), Value: 100},
		{Timestamp: time.Now(), Value: 10},
	})
	if trend.Classification != "sudden_spike" {
		t.Fatalf("unexpected classification: %+v", trend)
	}
}

func TestClassifyTrendNoData(t *testing.T) {
	trend := classifyTrend(diagnostic.MetricTrend{Profile: "oom_memory_growth"}, nil)
	if trend.Classification != "no_data" {
		t.Fatalf("unexpected classification: %+v", trend)
	}
}
