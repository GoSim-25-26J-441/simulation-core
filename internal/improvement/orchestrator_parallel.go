package improvement

import (
	"context"
	"fmt"
	"sync"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// EvaluateConfigurationsParallel evaluates multiple configurations in parallel
func (o *Orchestrator) EvaluateConfigurationsParallel(ctx context.Context, scenarios []*config.Scenario, durationMs int64) ([]*ConfigurationCandidate, error) {
	if len(scenarios) == 0 {
		return nil, fmt.Errorf("no scenarios provided")
	}

	// Limit parallelism
	semaphore := make(chan struct{}, o.maxParallelRuns)
	var wg sync.WaitGroup
	results := make([]*ConfigurationCandidate, len(scenarios))
	errors := make([]error, len(scenarios))
	var mu sync.Mutex

	for i, scenario := range scenarios {
		wg.Add(1)
		go func(idx int, sc *config.Scenario) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Evaluate configuration
			score, err := o.evaluateConfiguration(ctx, sc, durationMs)
			mu.Lock()
			if err != nil {
				errors[idx] = err
				results[idx] = &ConfigurationCandidate{
					Config:    sc,
					Evaluated: false,
				}
			} else {
				// Get metrics for this configuration
				runCtx := o.findRunContextForConfig(sc)
				var metrics *simulationv1.RunMetrics
				if runCtx != nil && runCtx.Metrics != nil {
					metrics = runCtx.Metrics
				}

				results[idx] = &ConfigurationCandidate{
					Config:    sc,
					Score:     score,
					Evaluated: true,
					Metrics:   metrics,
				}
			}
			mu.Unlock()
		}(i, scenario)
	}

	wg.Wait()

	// Check for errors
	for _, err := range errors {
		if err != nil {
			// Return first error, but still return partial results
			return results, fmt.Errorf("some configurations failed to evaluate: %w", err)
		}
	}

	return results, nil
}

// CancelActiveRuns cancels all active optimization runs
func (o *Orchestrator) CancelActiveRuns() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	var errors []error
	for runID := range o.activeRuns {
		_, err := o.executor.Stop(runID)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed to stop run %s: %w", runID, err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("errors cancelling runs: %v", errors)
	}

	return nil
}

// GetActiveRunCount returns the number of currently active runs
func (o *Orchestrator) GetActiveRunCount() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return len(o.activeRuns)
}

// CleanupCompletedRuns removes completed runs from active tracking
func (o *Orchestrator) CleanupCompletedRuns() {
	o.mu.Lock()
	defer o.mu.Unlock()

	for runID, runCtx := range o.activeRuns {
		if runCtx.Status == RunStatusCompleted || runCtx.Status == RunStatusFailed {
			delete(o.activeRuns, runID)
		}
	}
}
