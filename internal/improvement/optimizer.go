package improvement

import (
	"fmt"
	"math"
	"sync"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// Optimizer implements a hill-climbing optimization algorithm
type Optimizer struct {
	objective     ObjectiveFunction
	maxIterations int
	stepSize      float64           // Step size for parameter adjustments
	explorer      ParameterExplorer // Parameter space exploration strategy
	mu            sync.RWMutex
	bestScore     float64
	bestConfig    *config.Scenario
	iteration     int
	history       []OptimizationStep
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
		objective:     objective,
		maxIterations: maxIterations,
		stepSize:      stepSize,
		explorer:      NewDefaultExplorer(), // Use default exploration strategy
		bestScore:     math.MaxFloat64,      // Start with worst possible score
		history:       make([]OptimizationStep, 0),
	}
}

// WithExplorer sets a custom parameter exploration strategy
func (o *Optimizer) WithExplorer(explorer ParameterExplorer) *Optimizer {
	o.explorer = explorer
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
	o.mu.Unlock()

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

		// Evaluate all neighbors and find the best one
		bestNeighbor := neighbors[0]
		bestNeighborScore := math.MaxFloat64
		improved := false

		for _, neighbor := range neighbors {
			score, err := evaluateFunc(neighbor)
			if err != nil {
				// Skip invalid configurations
				continue
			}

			if score < bestNeighborScore {
				bestNeighborScore = score
				bestNeighbor = neighbor
			}
		}

		// Check if we found a better configuration
		if bestNeighborScore < currentScore {
			// Move to the better neighbor
			currentConfig = bestNeighbor
			currentScore = bestNeighborScore
			improved = true

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
			o.mu.Unlock()
		} else {
			// No improvement found, check for convergence
			o.mu.Lock()
			o.history = append(o.history, OptimizationStep{
				Iteration: iteration,
				Score:     currentScore,
				Config:    cloneScenario(currentConfig),
			})
			o.mu.Unlock()

			// If no improvement for several iterations, we might have converged
			// For now, we'll continue until max iterations
		}

		// Early stopping if no improvement (optional - can be made configurable)
		if !improved && iteration > 3 {
			// Check if we've had no improvement for a while
			// This is a simple convergence check
			recentImprovements := 0
			for i := len(o.history) - 1; i >= 0 && i >= len(o.history)-3; i-- {
				if i > 0 && o.history[i].Score < o.history[i-1].Score {
					recentImprovements++
				}
			}
			if recentImprovements == 0 {
				return o.buildResult(true, "no improvement in recent iterations"), nil
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

// cloneScenario creates a deep copy of a scenario
func cloneScenario(scenario *config.Scenario) *config.Scenario {
	if scenario == nil {
		return nil
	}

	cloned := &config.Scenario{
		Hosts:    make([]config.Host, len(scenario.Hosts)),
		Services: make([]config.Service, len(scenario.Services)),
		Workload: make([]config.WorkloadPattern, len(scenario.Workload)),
	}

	// Copy hosts
	for i, host := range scenario.Hosts {
		cloned.Hosts[i] = config.Host{
			ID:    host.ID,
			Cores: host.Cores,
		}
	}

	// Copy services
	for i, svc := range scenario.Services {
		cloned.Services[i] = config.Service{
			ID:        svc.ID,
			Replicas:  svc.Replicas,
			Model:     svc.Model,
			CPUCores:  svc.CPUCores,
			MemoryMB:  svc.MemoryMB,
			Endpoints: make([]config.Endpoint, len(svc.Endpoints)),
		}

		// Copy endpoints
		for j, ep := range svc.Endpoints {
			cloned.Services[i].Endpoints[j] = config.Endpoint{
				Path:            ep.Path,
				MeanCPUMs:       ep.MeanCPUMs,
				CPUSigmaMs:      ep.CPUSigmaMs,
				DefaultMemoryMB: ep.DefaultMemoryMB,
				NetLatencyMs:    ep.NetLatencyMs,
				Downstream:      make([]config.DownstreamCall, len(ep.Downstream)),
			}

			// Copy downstream calls
			for k, ds := range ep.Downstream {
				cloned.Services[i].Endpoints[j].Downstream[k] = config.DownstreamCall{
					To:                    ds.To,
					CallCountMean:         ds.CallCountMean,
					CallLatencyMs:         ds.CallLatencyMs,
					DownstreamFractionCPU: ds.DownstreamFractionCPU,
				}
			}
		}
	}

	// Copy workload patterns
	for i, wl := range scenario.Workload {
		cloned.Workload[i] = config.WorkloadPattern{
			From:    wl.From,
			To:      wl.To,
			Arrival: wl.Arrival,
		}
	}

	// Copy policies if they exist
	if scenario.Policies != nil {
		cloned.Policies = &config.Policies{}
		if scenario.Policies.Autoscaling != nil {
			cloned.Policies.Autoscaling = &config.AutoscalingPolicy{
				Enabled:       scenario.Policies.Autoscaling.Enabled,
				TargetCPUUtil: scenario.Policies.Autoscaling.TargetCPUUtil,
				ScaleStep:     scenario.Policies.Autoscaling.ScaleStep,
			}
		}
		if scenario.Policies.Retries != nil {
			cloned.Policies.Retries = &config.RetryPolicy{
				Enabled:    scenario.Policies.Retries.Enabled,
				MaxRetries: scenario.Policies.Retries.MaxRetries,
				Backoff:    scenario.Policies.Retries.Backoff,
				BaseMs:     scenario.Policies.Retries.BaseMs,
			}
		}
	}

	return cloned
}
