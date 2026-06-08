package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// LiveResourceName returns a stable Kubernetes-safe name without exposing the
// case ID or expected fault type to the system under evaluation.
func LiveResourceName(caseID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(caseID)))
	return "ks-" + hex.EncodeToString(sum[:])[:12]
}
