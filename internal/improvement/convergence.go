package improvement

import (
	"fmt"
	"math"
)

// ConvergenceStrategy defines how to detect convergence
type ConvergenceStrategy interface {
	// CheckConvergence checks if optimization has converged based on history
	CheckConvergence(history []OptimizationStep) (bool, string)
	// Name returns the name of the convergence strategy
	Name() string
}

// ConvergenceConfig holds configuration for convergence detection
type ConvergenceConfig struct {
	// NoImprovementIterations is the number of iterations without improvement before stopping
	NoImprovementIterations int
	// ImprovementThreshold is the minimum relative improvement to consider significant
	ImprovementThreshold float64
	// ScoreTolerance is the absolute tolerance for score changes to be considered equal
	ScoreTolerance float64
	// MinIterations is the minimum number of iterations before convergence can be detected
	MinIterations int
	// PlateauIterations is the number of iterations with similar scores (plateau) before stopping
	PlateauIterations int
}

// DefaultConvergenceConfig returns a default convergence configuration
func DefaultConvergenceConfig() *ConvergenceConfig {
	return &ConvergenceConfig{
		NoImprovementIterations: 5,
		ImprovementThreshold:    0.01, // 1% improvement
		ScoreTolerance:          0.001,
		MinIterations:           3,
		PlateauIterations:       5,
	}
}

// NoImprovementStrategy detects convergence when there's no improvement for N iterations
type NoImprovementStrategy struct {
	config *ConvergenceConfig
}

// NewNoImprovementStrategy creates a new no-improvement convergence strategy
func NewNoImprovementStrategy(config *ConvergenceConfig) *NoImprovementStrategy {
	if config == nil {
		config = DefaultConvergenceConfig()
	}
	return &NoImprovementStrategy{config: config}
}

func (s *NoImprovementStrategy) Name() string {
	return "no_improvement"
}

func (s *NoImprovementStrategy) CheckConvergence(history []OptimizationStep) (converged bool, reason string) {
	if len(history) < s.config.MinIterations {
		return false, ""
	}

	// Find the best score so far
	bestScore := math.MaxFloat64
	bestIteration := -1
	for i, step := range history {
		if step.Score < bestScore {
			bestScore = step.Score
			bestIteration = i
		}
	}

	if bestIteration < 0 {
		return false, ""
	}

	// Check if we've had no improvement in the last N iterations
	lastIteration := len(history) - 1
	iterationsSinceBest := lastIteration - bestIteration

	if iterationsSinceBest >= s.config.NoImprovementIterations {
		return true, fmt.Sprintf("no improvement for %d iterations (best at iteration %d)", iterationsSinceBest, bestIteration)
	}

	return false, ""
}

// PlateauStrategy detects convergence when scores have plateaued (similar scores)
type PlateauStrategy struct {
	config *ConvergenceConfig
}

// NewPlateauStrategy creates a new plateau convergence strategy
func NewPlateauStrategy(config *ConvergenceConfig) *PlateauStrategy {
	if config == nil {
		config = DefaultConvergenceConfig()
	}
	return &PlateauStrategy{config: config}
}

func (s *PlateauStrategy) Name() string {
	return "plateau"
}

func (s *PlateauStrategy) CheckConvergence(history []OptimizationStep) (converged bool, reason string) {
	if len(history) < s.config.MinIterations {
		return false, ""
	}

	// Check if the last N iterations have similar scores (within tolerance)
	if len(history) < s.config.PlateauIterations {
		return false, ""
	}

	recentSteps := history[len(history)-s.config.PlateauIterations:]
	minScore := recentSteps[0].Score
	maxScore := recentSteps[0].Score

	for _, step := range recentSteps {
		if step.Score < minScore {
			minScore = step.Score
		}
		if step.Score > maxScore {
			maxScore = step.Score
		}
	}

	scoreRange := maxScore - minScore
	if scoreRange <= s.config.ScoreTolerance {
		return true, fmt.Sprintf("score plateaued for %d iterations (range: %.6f)", s.config.PlateauIterations, scoreRange)
	}

	return false, ""
}

// ThresholdStrategy detects convergence when improvements are below threshold
type ThresholdStrategy struct {
	config *ConvergenceConfig
}

// NewThresholdStrategy creates a new improvement threshold convergence strategy
func NewThresholdStrategy(config *ConvergenceConfig) *ThresholdStrategy {
	if config == nil {
		config = DefaultConvergenceConfig()
	}
	return &ThresholdStrategy{config: config}
}

func (s *ThresholdStrategy) Name() string {
	return "improvement_threshold"
}

func (s *ThresholdStrategy) CheckConvergence(history []OptimizationStep) (converged bool, reason string) {
	if len(history) < s.config.MinIterations+1 {
		return false, ""
	}

	// Check recent improvements
	recentSteps := history[len(history)-s.config.NoImprovementIterations:]
	if len(recentSteps) < 2 {
		return false, ""
	}

	// Calculate relative improvements
	improvements := make([]float64, 0)
	for i := 1; i < len(recentSteps); i++ {
		if recentSteps[i-1].Score > 0 {
			relativeImprovement := (recentSteps[i-1].Score - recentSteps[i].Score) / recentSteps[i-1].Score
			improvements = append(improvements, relativeImprovement)
		}
	}

	// Check if all recent improvements are below threshold
	allBelowThreshold := true
	for _, imp := range improvements {
		if imp > s.config.ImprovementThreshold {
			allBelowThreshold = false
			break
		}
	}

	if allBelowThreshold && len(improvements) > 0 {
		maxImprovement := improvements[0]
		for _, imp := range improvements {
			if imp > maxImprovement {
				maxImprovement = imp
			}
		}
		return true, fmt.Sprintf("improvements below threshold (max: %.4f%%, threshold: %.4f%%)", maxImprovement*100, s.config.ImprovementThreshold*100)
	}

	return false, ""
}

// CombinedStrategy uses multiple strategies and converges if any strategy detects convergence
type CombinedStrategy struct {
	strategies []ConvergenceStrategy
	config     *ConvergenceConfig
}

// NewCombinedStrategy creates a new combined convergence strategy
func NewCombinedStrategy(config *ConvergenceConfig) *CombinedStrategy {
	if config == nil {
		config = DefaultConvergenceConfig()
	}
	return &CombinedStrategy{
		strategies: []ConvergenceStrategy{
			NewNoImprovementStrategy(config),
			NewPlateauStrategy(config),
			NewThresholdStrategy(config),
		},
		config: config,
	}
}

func (s *CombinedStrategy) Name() string {
	return "combined"
}

func (s *CombinedStrategy) CheckConvergence(history []OptimizationStep) (converged bool, reason string) {
	// Check each strategy
	for _, strategy := range s.strategies {
		converged, reason := strategy.CheckConvergence(history)
		if converged {
			return true, fmt.Sprintf("%s: %s", strategy.Name(), reason)
		}
	}

	return false, ""
}

// AddStrategy adds a custom strategy to the combined strategy
func (s *CombinedStrategy) AddStrategy(strategy ConvergenceStrategy) {
	s.strategies = append(s.strategies, strategy)
}

// VarianceStrategy detects convergence when score variance is low (stable)
type VarianceStrategy struct {
	config *ConvergenceConfig
}

// NewVarianceStrategy creates a new variance-based convergence strategy
func NewVarianceStrategy(config *ConvergenceConfig) *VarianceStrategy {
	if config == nil {
		config = DefaultConvergenceConfig()
	}
	return &VarianceStrategy{config: config}
}

func (s *VarianceStrategy) Name() string {
	return "variance"
}

func (s *VarianceStrategy) CheckConvergence(history []OptimizationStep) (converged bool, reason string) {
	if len(history) < s.config.MinIterations {
		return false, ""
	}

	// Calculate variance of recent scores
	windowSize := s.config.PlateauIterations
	if len(history) < windowSize {
		windowSize = len(history)
	}

	recentSteps := history[len(history)-windowSize:]
	if len(recentSteps) < 2 {
		return false, ""
	}

	// Calculate mean
	mean := 0.0
	for _, step := range recentSteps {
		mean += step.Score
	}
	mean /= float64(len(recentSteps))

	// Calculate variance
	variance := 0.0
	for _, step := range recentSteps {
		diff := step.Score - mean
		variance += diff * diff
	}
	variance /= float64(len(recentSteps))

	// Check if variance is below threshold (relative to mean)
	if mean > 0 {
		relativeVariance := math.Sqrt(variance) / mean
		if relativeVariance < s.config.ImprovementThreshold {
			return true, fmt.Sprintf("low score variance (relative stddev: %.4f%%)", relativeVariance*100)
		}
	}

	return false, ""
}
