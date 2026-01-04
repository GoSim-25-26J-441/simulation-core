package improvement

import (
	"fmt"
	"math"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// SelectionStrategy defines how to select the best configuration from multiple candidates
type SelectionStrategy interface {
	// SelectBest chooses the best configuration from a list of candidates
	SelectBest(candidates []*ConfigurationCandidate, objective ObjectiveFunction) (*ConfigurationCandidate, error)
	// Name returns the name of the selection strategy
	Name() string
}

// ConfigurationCandidate represents a configuration with its evaluation results
type ConfigurationCandidate struct {
	Config    *config.Scenario
	RunID     string
	Metrics   *simulationv1.RunMetrics
	Score     float64
	Iteration int
	Evaluated bool
}

// BestScoreStrategy selects the configuration with the best (lowest) objective score
type BestScoreStrategy struct{}

func (s *BestScoreStrategy) Name() string {
	return "best_score"
}

func (s *BestScoreStrategy) SelectBest(candidates []*ConfigurationCandidate, objective ObjectiveFunction) (*ConfigurationCandidate, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no candidates provided")
	}
	if objective == nil {
		return nil, fmt.Errorf("objective function is required")
	}

	// Filter to only evaluated candidates
	evaluated := make([]*ConfigurationCandidate, 0)
	for _, cand := range candidates {
		if cand.Evaluated && cand.Metrics != nil {
			evaluated = append(evaluated, cand)
		}
	}

	if len(evaluated) == 0 {
		return nil, fmt.Errorf("no evaluated candidates")
	}

	// Find candidate with best (lowest) score
	best := evaluated[0]
	for i := 1; i < len(evaluated); i++ {
		if evaluated[i].Score < best.Score {
			best = evaluated[i]
		}
	}

	return best, nil
}

// ParetoOptimalStrategy selects configurations that are Pareto optimal
// (not dominated by any other configuration in all objectives)
type ParetoOptimalStrategy struct {
	secondaryObjectives []ObjectiveFunction
}

// NewParetoOptimalStrategy creates a new Pareto optimal selection strategy
func NewParetoOptimalStrategy(secondaryObjectives []ObjectiveFunction) *ParetoOptimalStrategy {
	return &ParetoOptimalStrategy{
		secondaryObjectives: secondaryObjectives,
	}
}

func (s *ParetoOptimalStrategy) Name() string {
	return "pareto_optimal"
}

func (s *ParetoOptimalStrategy) SelectBest(candidates []*ConfigurationCandidate, objective ObjectiveFunction) (*ConfigurationCandidate, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no candidates provided")
	}
	if objective == nil {
		return nil, fmt.Errorf("objective function is required")
	}

	// Filter to only evaluated candidates
	evaluated := make([]*ConfigurationCandidate, 0)
	for _, cand := range candidates {
		if cand.Evaluated && cand.Metrics != nil {
			evaluated = append(evaluated, cand)
		}
	}

	if len(evaluated) == 0 {
		return nil, fmt.Errorf("no evaluated candidates")
	}

	// Find Pareto optimal candidates
	// Note: primary objective is already evaluated and stored in candidate.Score
	paretoOptimal := findParetoOptimal(evaluated, s.secondaryObjectives)

	if len(paretoOptimal) == 0 {
		// Fallback to best score if no Pareto optimal found
		strategy := &BestScoreStrategy{}
		return strategy.SelectBest(candidates, objective)
	}

	// If multiple Pareto optimal, prefer the one with best primary objective
	best := paretoOptimal[0]
	for i := 1; i < len(paretoOptimal); i++ {
		if paretoOptimal[i].Score < best.Score {
			best = paretoOptimal[i]
		}
	}

	return best, nil
}

// findParetoOptimal finds configurations that are not dominated by any other
// Note: primary objective is already evaluated and stored in candidate.Score
func findParetoOptimal(candidates []*ConfigurationCandidate, secondary []ObjectiveFunction) []*ConfigurationCandidate {
	if len(candidates) == 0 {
		return nil
	}

	paretoOptimal := make([]*ConfigurationCandidate, 0)

	for _, candidate := range candidates {
		isDominated := false

		// Check if this candidate is dominated by any other
		for _, other := range candidates {
			if candidate == other {
				continue
			}

			if dominates(other, candidate, secondary) {
				isDominated = true
				break
			}
		}

		if !isDominated {
			paretoOptimal = append(paretoOptimal, candidate)
		}
	}

	return paretoOptimal
}

// dominates checks if candidate1 dominates candidate2
// candidate1 dominates candidate2 if it's better or equal in all objectives and better in at least one
// Note: primary objective is already evaluated and stored in candidate.Score
func dominates(candidate1, candidate2 *ConfigurationCandidate, secondary []ObjectiveFunction) bool {
	// Check primary objective (already evaluated and stored in Score)
	primaryBetter := candidate1.Score <= candidate2.Score
	primaryEqual := math.Abs(candidate1.Score-candidate2.Score) < 0.0001

	if !primaryBetter && !primaryEqual {
		return false // candidate1 is worse in primary objective
	}

	// Check secondary objectives
	allBetterOrEqual := primaryBetter || primaryEqual
	atLeastOneBetter := primaryBetter

	for _, obj := range secondary {
		score1, err1 := obj.Evaluate(candidate1.Metrics)
		score2, err2 := obj.Evaluate(candidate2.Metrics)

		if err1 != nil || err2 != nil {
			continue // Skip if evaluation fails
		}

		better := score1 <= score2
		equal := math.Abs(score1-score2) < 0.0001

		if !better && !equal {
			return false // candidate1 is worse in this objective
		}

		allBetterOrEqual = allBetterOrEqual && (better || equal)
		atLeastOneBetter = atLeastOneBetter || better
	}

	return allBetterOrEqual && atLeastOneBetter
}

// BalancedStrategy selects configuration that balances multiple objectives
// Uses a weighted sum approach
type BalancedStrategy struct {
	weights map[string]float64 // objective name -> weight
}

// NewBalancedStrategy creates a new balanced selection strategy
func NewBalancedStrategy(weights map[string]float64) *BalancedStrategy {
	return &BalancedStrategy{
		weights: weights,
	}
}

func (s *BalancedStrategy) Name() string {
	return "balanced"
}

func (s *BalancedStrategy) SelectBest(candidates []*ConfigurationCandidate, objective ObjectiveFunction) (*ConfigurationCandidate, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no candidates provided")
	}
	if objective == nil {
		return nil, fmt.Errorf("objective function is required")
	}

	// Filter to only evaluated candidates
	evaluated := make([]*ConfigurationCandidate, 0)
	for _, cand := range candidates {
		if cand.Evaluated && cand.Metrics != nil {
			evaluated = append(evaluated, cand)
		}
	}

	if len(evaluated) == 0 {
		return nil, fmt.Errorf("no evaluated candidates")
	}

	// Calculate weighted scores
	best := evaluated[0]
	bestWeightedScore := s.calculateWeightedScore(best, objective)

	for i := 1; i < len(evaluated); i++ {
		weightedScore := s.calculateWeightedScore(evaluated[i], objective)
		if weightedScore < bestWeightedScore {
			bestWeightedScore = weightedScore
			best = evaluated[i]
		}
	}

	return best, nil
}

func (s *BalancedStrategy) calculateWeightedScore(candidate *ConfigurationCandidate, primary ObjectiveFunction) float64 {
	score := candidate.Score

	// Add weighted contributions from other objectives if specified
	for objName, weight := range s.weights {
		if objName == primary.Name() {
			continue // Skip primary objective
		}

		// Try to evaluate this objective
		obj, err := NewObjectiveFunction(objName)
		if err != nil {
			continue // Skip if objective not found
		}

		objScore, err := obj.Evaluate(candidate.Metrics)
		if err != nil {
			continue // Skip if evaluation fails
		}

		score += weight * objScore
	}

	return score
}

// SelectBestConfiguration is a convenience function that uses the default strategy
func SelectBestConfiguration(candidates []*ConfigurationCandidate, objective ObjectiveFunction) (*ConfigurationCandidate, error) {
	strategy := &BestScoreStrategy{}
	return strategy.SelectBest(candidates, objective)
}

// EvaluateCandidate evaluates a configuration candidate using the objective function
func EvaluateCandidate(candidate *ConfigurationCandidate, objective ObjectiveFunction) error {
	if candidate == nil {
		return fmt.Errorf("candidate is nil")
	}
	if candidate.Metrics == nil {
		return fmt.Errorf("candidate metrics are nil")
	}
	if objective == nil {
		return fmt.Errorf("objective function is nil")
	}

	score, err := objective.Evaluate(candidate.Metrics)
	if err != nil {
		return fmt.Errorf("failed to evaluate objective: %w", err)
	}

	candidate.Score = score
	candidate.Evaluated = true
	return nil
}
