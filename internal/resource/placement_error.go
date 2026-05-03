package resource

import (
	"errors"
	"strings"
)

// ErrPlacementInfeasible indicates that instances cannot be packed onto hosts under the
// scenario's placement rules (zones, affinity, anti-affinity, per-host capacity).
var ErrPlacementInfeasible = errors.New("placement infeasible")

// RunFailureMessageIndicatesPlacementInfeasible classifies persisted Run.error strings from
// the executor (which wraps InitializeFromScenario failures as plain text).
func RunFailureMessageIndicatesPlacementInfeasible(msg string) bool {
	if msg == "" {
		return false
	}
	low := strings.ToLower(msg)
	if strings.Contains(low, "placement infeasible") {
		return true
	}
	if !strings.Contains(low, "resource initialization failed") {
		return false
	}
	return strings.Contains(low, "cannot place service") ||
		strings.Contains(low, "insufficient host capacity") ||
		strings.Contains(low, "no hosts defined")
}
