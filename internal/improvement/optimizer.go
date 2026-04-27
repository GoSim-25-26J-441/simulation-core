package improvement

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// ProgressReporter is called with optimization progress (iteration, bestScore).
type ProgressReporter func(iteration int, bestScore float64)

// Optimizer implements a hill-climbing optimization algorithm
type Optimizer struct {
	objective           ObjectiveFunction
	maxIterations       int
	maxEvaluations      int                 // optional cap on total evaluateFunc calls (0 = no cap)
	stepSize            float64             // Step size for parameter adjustments
	explorer            ParameterExplorer   // Parameter space exploration strategy
	convergenceStrategy ConvergenceStrategy // Convergence detection strategy
	progressReporter    ProgressReporter    // Optional: called each iteration
	mu                  sync.RWMutex
	bestScore           float64
	bestConfig          *config.Scenario
	iteration           int
	history             []OptimizationStep
}

// OptimizationStep represents a single optimization step
type OptimizationStep struct {
	Iteration int
	Score     float64
	Config    *config.Scenario
}

// OptimizationResult contains the final optimization result
type OptimizationResult struct {
	BestConfig        *config.Scenario
	BestScore         float64
	Iterations        int
	History           []OptimizationStep
	Converged         bool
	ConvergenceReason string
}

// NewOptimizer creates a new hill-climbing optimizer
func NewOptimizer(objective ObjectiveFunction, maxIterations int, stepSize float64) *Optimizer {
	if stepSize <= 0 {
		stepSize = 1.0 // Default step size
	}
	return &Optimizer{
		objective:           objective,
		maxIterations:       maxIterations,
		stepSize:            stepSize,
		explorer:            NewDefaultExplorer(),                            // Use default exploration strategy
		convergenceStrategy: NewCombinedStrategy(DefaultConvergenceConfig()), // Use combined convergence strategy
		bestScore:           math.MaxFloat64,                                 // Start with worst possible score
		history:             make([]OptimizationStep, 0),
	}
}

// WithExplorer sets a custom parameter exploration strategy
func (o *Optimizer) WithExplorer(explorer ParameterExplorer) *Optimizer {
	o.explorer = explorer
	return o
}

// WithConvergenceStrategy sets a custom convergence detection strategy
func (o *Optimizer) WithConvergenceStrategy(strategy ConvergenceStrategy) *Optimizer {
	o.convergenceStrategy = strategy
	return o
}

// WithProgressReporter sets an optional callback for per-iteration progress
func (o *Optimizer) WithProgressReporter(reporter ProgressReporter) *Optimizer {
	o.progressReporter = reporter
	return o
}

// WithMaxEvaluations sets an optional cap on total evaluation runs. When > 0,
// Optimize stops after this many evaluateFunc calls (initial config + neighbors).
// When 0, only maxIterations limits the number of improvement steps.
func (o *Optimizer) WithMaxEvaluations(n int) *Optimizer {
	if n < 0 {
		n = 0
	}
	o.maxEvaluations = n
	return o
}

// Optimize runs the hill-climbing optimization algorithm
func (o *Optimizer) Optimize(initialConfig *config.Scenario, evaluateFunc func(*config.Scenario) (float64, error)) (*OptimizationResult, error) {
	if initialConfig == nil {
		return nil, fmt.Errorf("initial configuration is required")
	}
	if evaluateFunc == nil {
		return nil, fmt.Errorf("evaluation function is required")
	}

	o.mu.Lock()
	o.bestConfig = cloneScenario(initialConfig)
	o.iteration = 0
	o.history = make([]OptimizationStep, 0)
	o.mu.Unlock()

	// Evaluate initial configuration
	initialScore, err := evaluateFunc(initialConfig)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to evaluate initial configuration: %w", err)
	}

	o.mu.Lock()
	o.bestScore = initialScore
	o.history = append(o.history, OptimizationStep{
		Iteration: 0,
		Score:     initialScore,
		Config:    cloneScenario(initialConfig),
	})
	currentConfig := cloneScenario(initialConfig)
	currentScore := initialScore
	rep := o.progressReporter
	o.mu.Unlock()

	if rep != nil {
		rep(0, initialScore)
	}

	evaluations := 1 // initial config already evaluated

	// Hill-climbing iterations
	for iteration := 1; iteration <= o.maxIterations; iteration++ {
		o.mu.Lock()
		o.iteration = iteration
		o.mu.Unlock()

		// Generate neighbor configurations
		neighbors := o.generateNeighbors(currentConfig)
		if len(neighbors) == 0 {
			// No valid neighbors, optimization converged
			return o.buildResult(true, "no valid neighbors"), nil
		}

		// Evaluate all neighbors and find the best one (respect maxEvaluations cap)
		bestNeighbor := neighbors[0]
		bestNeighborScore := math.MaxFloat64
		hitEvalCap := false

		for _, neighbor := range neighbors {
			if o.maxEvaluations > 0 && evaluations >= o.maxEvaluations {
				hitEvalCap = true
				break
			}
			evaluations++
			score, err := evaluateFunc(neighbor)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return nil, err
				}
				// Skip invalid configurations
				continue
			}

			if score < bestNeighborScore {
				bestNeighborScore = score
				bestNeighbor = neighbor
			}
		}
		if hitEvalCap {
			return o.buildResult(false, "max evaluations reached"), nil
		}

		// Check if we found a better configuration
		if bestNeighborScore < currentScore {
			// Move to the better neighbor
			currentConfig = bestNeighbor
			currentScore = bestNeighborScore

			// Update best if this is the best so far
			o.mu.Lock()
			if currentScore < o.bestScore {
				o.bestScore = currentScore
				o.bestConfig = cloneScenario(currentConfig)
			}
			o.history = append(o.history, OptimizationStep{
				Iteration: iteration,
				Score:     currentScore,
				Config:    cloneScenario(currentConfig),
			})
			rep := o.progressReporter
			o.mu.Unlock()
			if rep != nil {
				rep(iteration, currentScore)
			}
		} else {
			// No improvement found, record the iteration
			o.mu.Lock()
			o.history = append(o.history, OptimizationStep{
				Iteration: iteration,
				Score:     currentScore,
				Config:    cloneScenario(currentConfig),
			})
			historyCopy := make([]OptimizationStep, len(o.history))
			copy(historyCopy, o.history)
			rep := o.progressReporter
			o.mu.Unlock()

			if rep != nil {
				rep(iteration, currentScore)
			}

			// Check for convergence using the configured strategy
			if o.convergenceStrategy != nil {
				converged, reason := o.convergenceStrategy.CheckConvergence(historyCopy)
				if converged {
					return o.buildResult(true, reason), nil
				}
			}
		}
	}

	return o.buildResult(false, "max iterations reached"), nil
}

// generateNeighbors generates neighboring configurations using the configured explorer
func (o *Optimizer) generateNeighbors(scenario *config.Scenario) []*config.Scenario {
	if o.explorer == nil {
		// Fallback to default explorer if none configured
		o.explorer = NewDefaultExplorer()
	}
	return o.explorer.GenerateNeighbors(scenario, o.stepSize)
}

// buildResult constructs the optimization result
func (o *Optimizer) buildResult(converged bool, reason string) *OptimizationResult {
	o.mu.RLock()
	defer o.mu.RUnlock()

	return &OptimizationResult{
		BestConfig:        cloneScenario(o.bestConfig),
		BestScore:         o.bestScore,
		Iterations:        o.iteration,
		History:           o.history,
		Converged:         converged,
		ConvergenceReason: reason,
	}
}

// GetBestConfig returns the best configuration found so far
func (o *Optimizer) GetBestConfig() *config.Scenario {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return cloneScenario(o.bestConfig)
}

// GetBestScore returns the best score found so far
func (o *Optimizer) GetBestScore() float64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.bestScore
}

// GetIteration returns the current iteration number
func (o *Optimizer) GetIteration() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.iteration
}
