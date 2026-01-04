package interaction

import (
	"fmt"
	"math"
	"math/rand"
)

// BranchingStrategy determines which downstream calls to make based on probabilities
type BranchingStrategy interface {
	// SelectCalls selects which downstream calls to make from a list of possible calls
	SelectCalls(calls []ResolvedCall, rng *rand.Rand) []ResolvedCall
}

// DefaultBranchingStrategy uses call_count_mean to determine number of calls
type DefaultBranchingStrategy struct{}

// SelectCalls selects downstream calls based on call_count_mean
func (s *DefaultBranchingStrategy) SelectCalls(calls []ResolvedCall, rng *rand.Rand) []ResolvedCall {
	if len(calls) == 0 {
		return nil
	}

	selected := make([]ResolvedCall, 0)

	for _, call := range calls {
		// Use call_count_mean to determine how many times to call this downstream service
		countMean := call.Call.CallCountMean
		if countMean <= 0 {
			countMean = 1.0 // Default to 1 call if not specified
		}

		// Stochastic rounding: use integer part plus one extra with probability equal to the fractional part
		base := int(math.Floor(countMean))
		frac := countMean - float64(base)
		count := base
		if frac > 0 && rng.Float64() < frac {
			count++
		}

		// Add the call count times
		for i := 0; i < count; i++ {
			selected = append(selected, call)
		}
	}

	return selected
}

// ProbabilisticBranchingStrategy uses explicit probabilities for each call
type ProbabilisticBranchingStrategy struct {
	probabilities map[string]float64 // "serviceID:path" -> probability
}

// NewProbabilisticBranchingStrategy creates a new probabilistic branching strategy
func NewProbabilisticBranchingStrategy(probabilities map[string]float64) *ProbabilisticBranchingStrategy {
	return &ProbabilisticBranchingStrategy{
		probabilities: probabilities,
	}
}

// SelectCalls selects downstream calls based on probabilities
func (s *ProbabilisticBranchingStrategy) SelectCalls(calls []ResolvedCall, rng *rand.Rand) []ResolvedCall {
	selected := make([]ResolvedCall, 0)

	for _, call := range calls {
		key := fmt.Sprintf("%s:%s", call.ServiceID, call.Path)
		prob, ok := s.probabilities[key]
		if !ok {
			// If no probability specified, use call_count_mean as fallback
			countMean := call.Call.CallCountMean
			if countMean <= 0 {
				countMean = 1.0
			}
			prob = math.Min(countMean, 1.0) // Cap at 1.0 for probability
		}

		if rng.Float64() < prob {
			selected = append(selected, call)
		}
	}

	return selected
}
