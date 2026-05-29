package rag

import (
	"context"
	"sort"
	"strings"
)

type SimpleKeywordRetriever struct {
	runbooks []Runbook
}

func NewSimpleKeywordRetriever(dir string) (*SimpleKeywordRetriever, error) {
	runbooks, err := LoadRunbooks(dir)
	if err != nil {
		return nil, err
	}
	return &SimpleKeywordRetriever{runbooks: runbooks}, nil
}

func (r *SimpleKeywordRetriever) Retrieve(ctx context.Context, faultType, query string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 3
	}
	keywords := tokenize(faultType + " " + query)
	hits := []Hit{}
	for _, runbook := range r.runbooks {
		for _, section := range runbook.Sections {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			score := scoreSection(runbook.FaultType, section, faultType, keywords)
			if score == 0 {
				continue
			}
			hits = append(hits, Hit{
				Title:   runbook.FaultType + " / " + section.Title,
				Content: strings.TrimSpace(section.Content),
				Score:   score,
			})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > limit {
		return hits[:limit], nil
	}
	return hits, nil
}

func scoreSection(runbookFault string, section Section, faultType string, keywords []string) int {
	score := 0
	if normalize(runbookFault) == normalize(faultType) {
		score += 5
	}
	text := strings.ToLower(section.Title + "\n" + section.Content)
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			score++
		}
	}
	return score
}

func tokenize(text string) []string {
	parts := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '/' || r == '_' || r == '-' || r == ':' || r == '\n'
	})
	result := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) >= 3 {
			result = append(result, part)
		}
	}
	return result
}

func normalize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return s
}
