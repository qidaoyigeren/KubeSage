package diagnostic

import (
	"fmt"
	"sort"
	"strings"
)

type Analyzer interface {
	Name() string
	// Match only decides whether this analyzer should participate in RCA.
	Match(ctx *DiagnosticContext) bool
	// Analyze generates evidence and suggestions without changing cluster state.
	Analyze(ctx *DiagnosticContext) (*AnalyzeResult, error)
}

type AnalyzerMetadata struct {
	Name             string   `json:"name"`
	FaultType        string   `json:"fault_type"`
	Priority         int      `json:"priority"`
	MatchSignals     []string `json:"match_signals"`
	RequiredEvidence []string `json:"required_evidence"`
}

type PluginAnalyzer interface {
	Analyzer
	Metadata() AnalyzerMetadata
}

type AnalyzerRegistry struct {
	items []registeredAnalyzer
}

type registeredAnalyzer struct {
	analyzer Analyzer
	metadata AnalyzerMetadata
}

func NewAnalyzerRegistry() *AnalyzerRegistry {
	return &AnalyzerRegistry{}
}

func (r *AnalyzerRegistry) Register(analyzer Analyzer, metadata AnalyzerMetadata) error {
	if analyzer == nil {
		return fmt.Errorf("analyzer is nil")
	}
	name := strings.TrimSpace(metadata.Name)
	if name == "" {
		name = analyzer.Name()
	}
	if name == "" {
		return fmt.Errorf("analyzer name is empty")
	}
	for _, item := range r.items {
		if item.metadata.Name == name {
			return fmt.Errorf("analyzer %s already registered", name)
		}
	}
	metadata.Name = name
	r.items = append(r.items, registeredAnalyzer{analyzer: analyzer, metadata: metadata})
	return nil
}

func (r *AnalyzerRegistry) RegisterPlugin(analyzer PluginAnalyzer) error {
	if analyzer == nil {
		return fmt.Errorf("analyzer is nil")
	}
	return r.Register(analyzer, analyzer.Metadata())
}

func (r *AnalyzerRegistry) Analyzers() []Analyzer {
	if r == nil {
		return nil
	}
	items := append([]registeredAnalyzer(nil), r.items...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].metadata.Priority == items[j].metadata.Priority {
			return items[i].metadata.Name < items[j].metadata.Name
		}
		return items[i].metadata.Priority > items[j].metadata.Priority
	})
	result := make([]Analyzer, 0, len(items))
	for _, item := range items {
		result = append(result, item.analyzer)
	}
	return result
}

func (r *AnalyzerRegistry) Metadata() []AnalyzerMetadata {
	if r == nil {
		return nil
	}
	items := append([]registeredAnalyzer(nil), r.items...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].metadata.Priority == items[j].metadata.Priority {
			return items[i].metadata.Name < items[j].metadata.Name
		}
		return items[i].metadata.Priority > items[j].metadata.Priority
	})
	result := make([]AnalyzerMetadata, 0, len(items))
	for _, item := range items {
		result = append(result, item.metadata)
	}
	return result
}
