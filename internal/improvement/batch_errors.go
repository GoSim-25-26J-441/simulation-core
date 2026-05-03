package improvement

import (
	"errors"
	"strings"

	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
)

// ErrBatchBudgetExhausted is returned when max_evaluations would be exceeded.
var ErrBatchBudgetExhausted = errors.New("batch evaluation budget exhausted")

// ErrBatchCandidateInfeasible marks neighbor candidates that cannot be placed or initialized
// as resources; RunBatchBeamSearch treats these as nonfatal for neighbor evaluations.
var ErrBatchCandidateInfeasible = errors.New("batch candidate placement infeasible")

// IsBatchCandidateInfeasibleError reports whether err indicates a placement / resource
// initialization failure for a neighbor scenario (simulation never starts meaningfully).
func IsBatchCandidateInfeasibleError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrBatchCandidateInfeasible) {
		return true
	}
	if errors.Is(err, resource.ErrPlacementInfeasible) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "resource initialization failed") ||
		strings.Contains(msg, "cannot place service") ||
		strings.Contains(msg, "insufficient host capacity")
}
