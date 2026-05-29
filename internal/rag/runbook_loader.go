package rag

import (
	"os"
	"path/filepath"
	"strings"
)

type Runbook struct {
	FaultType string
	Path      string
	Content   string
	Sections  []Section
}

type Section struct {
	Title   string
	Content string
}

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
		runbooks = append(runbooks, Runbook{
			FaultType: strings.TrimSuffix(file.Name(), ".md"),
			Path:      path,
			Content:   content,
			Sections:  splitMarkdownSections(content),
		})
	}
	return runbooks, nil
}

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
