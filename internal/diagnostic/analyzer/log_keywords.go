package analyzer

import (
	"fmt"
	"strings"
	"time"

	"kubesage/internal/diagnostic"
)

type keyLogEvidenceRaw struct {
	ContainerName string    `json:"container_name"`
	Source        string    `json:"source"`
	Keywords      []string  `json:"keywords"`
	Matches       []string  `json:"matches"`
	FaultTime     time.Time `json:"fault_time,omitempty"`
	WindowStart   time.Time `json:"window_start,omitempty"`
	WindowEnd     time.Time `json:"window_end,omitempty"`
	Precise       bool      `json:"precise"`
	Fallback      string    `json:"fallback,omitempty"`
}

var (
	oomKilledLogKeywords   = []string{"out of memory", "oom", "GC overhead", "heap", "memory limit"}
	crashLoopLogKeywords   = []string{"panic", "fatal", "config", "missing", "connection refused", "permission denied"}
	probeFailedLogKeywords = []string{"health", "timeout", "connection refused", "404", "503"}
)

// keyLogEvidences extracts keyword-matching log lines and stores them as a
// separate evidence type so reports can show key log fragments directly.
func keyLogEvidences(logs []diagnostic.ContainerLogs, title string, keywords []string, severity string) []diagnostic.EvidenceRecord {
	evidences := make([]diagnostic.EvidenceRecord, 0)
	for _, item := range logs {
		for _, source := range []struct {
			name string
			text string
		}{
			{name: "previous", text: item.Previous},
			{name: "current", text: item.Current},
		} {
			matches := matchingLogLines(source.text, keywords, 12)
			if len(matches) == 0 {
				continue
			}
			raw := keyLogEvidenceRaw{
				ContainerName: item.ContainerName,
				Source:        source.name,
				Keywords:      keywords,
				Matches:       matches,
				FaultTime:     item.FaultTime,
				WindowStart:   item.WindowStart,
				WindowEnd:     item.WindowEnd,
				Precise:       item.Precise,
				Fallback:      item.Fallback,
			}
			evidences = append(evidences, diagnostic.EvidenceRecord{
				SourceType: "k8s_key_log",
				Title:      title,
				Content:    keyLogContent(raw),
				Severity:   severity,
				Raw:        raw,
				Timestamp:  evidenceTimestamp(item),
			})
		}
	}
	return evidences
}

// matchingLogLines returns up to limit lines that contain any keyword.
func matchingLogLines(text string, keywords []string, limit int) []string {
	lines := strings.Split(text, "\n")
	matches := make([]string, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if containsAny(line, keywords) {
			matches = append(matches, line)
			if limit > 0 && len(matches) >= limit {
				return matches
			}
		}
	}
	return matches
}

// keyLogContent formats matched log lines with window metadata for evidence.
func keyLogContent(raw keyLogEvidenceRaw) string {
	window := "latest logs fallback"
	if raw.Precise {
		window = fmt.Sprintf("%s..%s", raw.WindowStart.Format(time.RFC3339), raw.WindowEnd.Format(time.RFC3339))
	}
	content := fmt.Sprintf(
		"container=%s source=%s window=%s keywords=%v\n%s",
		raw.ContainerName,
		raw.Source,
		window,
		raw.Keywords,
		strings.Join(raw.Matches, "\n"),
	)
	if len(content) > 4000 {
		content = content[:4000]
	}
	return content
}

// evidenceTimestamp chooses the fault time when available, otherwise now.
func evidenceTimestamp(logs diagnostic.ContainerLogs) time.Time {
	if !logs.FaultTime.IsZero() {
		return logs.FaultTime
	}
	return time.Now()
}
