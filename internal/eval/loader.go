package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadCases loads all case YAML files from a file or directory. Fixture paths
// are resolved relative to the case file for portable repositories.
func LoadCases(path string) ([]EvalCase, error) {
	paths, err := caseFiles(path)
	if err != nil {
		return nil, err
	}
	cases := make([]EvalCase, 0, len(paths))
	for _, file := range paths {
		loaded, err := loadCaseFile(file)
		if err != nil {
			return nil, err
		}
		cases = append(cases, loaded...)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no eval cases found in %s", path)
	}
	return cases, nil
}

func caseFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat cases path %s: %w", path, err)
	}
	if !info.IsDir() {
		return []string{path}, nil
	}
	files := []string{}
	err = filepath.WalkDir(path, func(candidate string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(candidate))
		if ext == ".yaml" || ext == ".yml" {
			files = append(files, candidate)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk cases path %s: %w", path, err)
	}
	sort.Strings(files)
	return files, nil
}

func loadCaseFile(path string) ([]EvalCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read case %s: %w", path, err)
	}
	var suite struct {
		Cases []EvalCase `yaml:"cases"`
	}
	if err := yaml.Unmarshal(data, &suite); err != nil {
		return nil, fmt.Errorf("parse case %s: %w", path, err)
	}
	cases := suite.Cases
	if len(cases) == 0 {
		var single EvalCase
		if err := yaml.Unmarshal(data, &single); err != nil {
			return nil, fmt.Errorf("parse case %s: %w", path, err)
		}
		if single.ID != "" {
			cases = []EvalCase{single}
		}
	}
	dir := filepath.Dir(path)
	for i := range cases {
		cases[i].SourcePath = path
		if err := validateCase(cases[i]); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if cases[i].Fixture != "" && !filepath.IsAbs(cases[i].Fixture) {
			cases[i].Fixture = filepath.Clean(filepath.Join(dir, cases[i].Fixture))
		}
	}
	return cases, nil
}

func validateCase(c EvalCase) error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("case id is required")
	}
	if strings.TrimSpace(c.Fixture) == "" {
		return fmt.Errorf("case %s fixture is required", c.ID)
	}
	if strings.TrimSpace(c.GoldenAnswer.ExpectedFaultType) == "" {
		return fmt.Errorf("case %s golden_answer.expected_fault_type is required", c.ID)
	}
	if strings.TrimSpace(c.GoldenAnswer.RootCause) == "" {
		return fmt.Errorf("case %s golden_answer.root_cause is required", c.ID)
	}
	if c.GoldenAnswer.ConfidenceMin < 0 || c.GoldenAnswer.ConfidenceMin > 1 {
		return fmt.Errorf("case %s confidence_min must be between 0 and 1", c.ID)
	}
	if c.GoldenAnswer.ConfidenceMax < 0 || c.GoldenAnswer.ConfidenceMax > 1 {
		return fmt.Errorf("case %s confidence_max must be between 0 and 1", c.ID)
	}
	return nil
}
