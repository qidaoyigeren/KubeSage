package rag

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Runbook struct {
	FaultType string
	Path      string
	Content   string
	Hints     RunbookHints
	Sections  []Section
}

type RunbookHints struct {
	FaultType             string            `json:"fault_type,omitempty" yaml:"fault_type"`
	Checks                []string          `json:"checks,omitempty" yaml:"checks"`
	RecommendedTools      []string          `json:"recommended_tools,omitempty" yaml:"recommended_tools"`
	RemediationCandidates []string          `json:"remediation_candidates,omitempty" yaml:"remediation_candidates"`
	RiskPolicy            map[string]string `json:"risk_policy,omitempty" yaml:"risk_policy"`
	StopConditions        []string          `json:"stop_conditions,omitempty" yaml:"stop_conditions"`
}

type Section struct {
	Title   string
	Content string
}

// LoadRunbooks reads markdown runbook files and builds searchable runbook data.
func LoadRunbooks(dir string) ([]Runbook, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	runbooks := make([]Runbook, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, file.Name())
		bytes, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		content := string(bytes)
		hints, body := parseFrontmatter(content)
		faultType := strings.TrimSuffix(file.Name(), ".md")
		if strings.TrimSpace(hints.FaultType) != "" {
			faultType = strings.TrimSpace(hints.FaultType)
		}
		sections := splitMarkdownSections(body)
		if hintSection := hints.Section(); hintSection.Content != "" {
			sections = append([]Section{hintSection}, sections...)
		}
		runbooks = append(runbooks, Runbook{
			FaultType: faultType,
			Path:      path,
			Content:   body,
			Hints:     hints,
			Sections:  sections,
		})
	}
	return runbooks, nil
}

// Section converts frontmatter into a searchable planner-hint section.
func (h RunbookHints) Section() Section {
	lines := []string{}
	addList := func(name string, values []string) {
		if len(values) == 0 {
			return
		}
		lines = append(lines, name+":")
		for _, value := range values {
			lines = append(lines, "- "+value)
		}
	}
	addList("checks", h.Checks)
	addList("recommended_tools", h.RecommendedTools)
	addList("remediation_candidates", h.RemediationCandidates)
	addList("stop_conditions", h.StopConditions)
	if len(h.RiskPolicy) > 0 {
		lines = append(lines, "risk_policy:")
		for key, value := range h.RiskPolicy {
			lines = append(lines, key+": "+value)
		}
	}
	return Section{Title: "planner_hints", Content: strings.Join(lines, "\n")}
}

func parseFrontmatter(content string) (RunbookHints, string) {
	trimmed := strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(trimmed, "---\n") && !strings.HasPrefix(trimmed, "---\r\n") {
		return RunbookHints{}, content
	}
	normalized := strings.ReplaceAll(trimmed, "\r\n", "\n")
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return RunbookHints{}, content
	}
	end += 4
	raw := normalized[4:end]
	body := strings.TrimPrefix(normalized[end:], "\n---")
	body = strings.TrimPrefix(body, "\n")
	var hints RunbookHints
	if err := yaml.Unmarshal([]byte(raw), &hints); err != nil {
		return RunbookHints{}, body
	}
	return hints, body
}

// splitMarkdownSections splits one markdown runbook into overview and heading
// sections.
func splitMarkdownSections(content string) []Section {
	lines := strings.Split(content, "\n")
	sections := []Section{}
	current := Section{Title: "overview"}
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if strings.TrimSpace(current.Content) != "" {
				sections = append(sections, current)
			}
			current = Section{Title: strings.TrimSpace(strings.TrimPrefix(line, "## "))}
			continue
		}
		current.Content += line + "\n"
	}
	if strings.TrimSpace(current.Content) != "" {
		sections = append(sections, current)
	}
	return sections
}
