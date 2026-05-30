package analyzer

import (
	"kubesage/internal/diagnostic"
	"kubesage/internal/prometheus"
)

func NewDefaultRegistry(prometheusClient *prometheus.Client) (*diagnostic.AnalyzerRegistry, error) {
	registry := diagnostic.NewAnalyzerRegistry()
	for _, item := range []diagnostic.PluginAnalyzer{
		NewOOMKilledAnalyzer(prometheusClient),
		NewCrashLoopBackOffAnalyzer(),
		NewImagePullBackOffAnalyzer(),
		NewInitErrorAnalyzer(),
		NewEvictedAnalyzer(),
		NewPendingAnalyzer(),
		NewProbeFailedAnalyzer(),
		NewNodeNotReadyAnalyzer(),
	} {
		if err := registry.RegisterPlugin(item); err != nil {
			return nil, err
		}
	}
	return registry, nil
}
