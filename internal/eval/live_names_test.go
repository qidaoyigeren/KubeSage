package eval

import (
	"strings"
	"testing"
)

func TestLiveResourceNameIsStableAndOpaque(t *testing.T) {
	name := LiveResourceName("bank-oom-low-limit")
	if name != LiveResourceName("bank-oom-low-limit") {
		t.Fatal("LiveResourceName must be deterministic")
	}
	if strings.Contains(name, "oom") || strings.Contains(name, "bank") {
		t.Fatalf("LiveResourceName(%q) leaked case semantics: %q", "bank-oom-low-limit", name)
	}
	if len(name) != 15 || !strings.HasPrefix(name, "ks-") {
		t.Fatalf("unexpected live resource name %q", name)
	}
}
