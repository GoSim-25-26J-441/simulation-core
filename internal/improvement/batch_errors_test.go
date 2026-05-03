package improvement

import (
	"errors"
	"fmt"
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
)

func TestIsBatchCandidateInfeasibleError_TypedPlacement(t *testing.T) {
	err := fmt.Errorf("run failed: %w", resource.ErrPlacementInfeasible)
	if !IsBatchCandidateInfeasibleError(err) {
		t.Fatal("expected typed placement infeasible to qualify")
	}
}

func TestIsBatchCandidateInfeasibleError_StringFallback(t *testing.T) {
	cases := []string{
		"resource initialization failed: cannot place service x",
		"run failed: cannot place service x: insufficient host capacity",
	}
	for _, msg := range cases {
		if !IsBatchCandidateInfeasibleError(errors.New(msg)) {
			t.Fatalf("expected string fallback for %q", msg)
		}
	}
}
