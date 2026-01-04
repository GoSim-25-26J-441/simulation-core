package improvement

import (
	"context"
	"fmt"
	"sync"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
)

// Orchestrator manages multi-run optimization experiments
type Orchestrator struct {
	store      *simd.RunStore
	executor   *simd.RunExecutor
	optimizer  *Optimizer
	objective  ObjectiveFunction
	mu         sync.RWMutex
	activeRuns map[string]*RunContext
}

// RunContext tracks the context for a single optimization run
type RunContext struct {
	RunID       string
	Config      *config.Scenario
	Status      RunStatus
	Score       float64
	Metrics     *simulationv1.RunMetrics
	Error       error
	CreatedAt   time.Time
	CompletedAt time.Time
}

// RunStatus represents the status of an optimization run
type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

// ExperimentResult contains the results of an optimization experiment
type ExperimentResult struct {
	BestConfig        *config.Scenario
	BestScore         float64
	BestRunID         string
	TotalRuns         int
	CompletedRuns     int
	FailedRuns        int
	Runs              []*RunContext
	Duration          time.Duration
	Converged         bool
	ConvergenceReason string
}

// NewOrchestrator creates a new optimization orchestrator
func NewOrchestrator(store *simd.RunStore, executor *simd.RunExecutor, optimizer *Optimizer, objective ObjectiveFunction) *Orchestrator {
	return &Orchestrator{
		store:      store,
		executor:   executor,
		optimizer:  optimizer,
		objective:  objective,
		activeRuns: make(map[string]*RunContext),
	}
}

// RunExperiment executes a full optimization experiment
func (o *Orchestrator) RunExperiment(ctx context.Context, initialConfig *config.Scenario, durationMs int64) (*ExperimentResult, error) {
	if initialConfig == nil {
		return nil, fmt.Errorf("initial configuration is required")
	}

	startTime := time.Now()
	result := &ExperimentResult{
		Runs: make([]*RunContext, 0),
	}

	// Create evaluation function that runs a simulation and returns the score
	evaluateFunc := func(scenario *config.Scenario) (float64, error) {
		return o.evaluateConfiguration(ctx, scenario, durationMs)
	}

	// Run optimization
	optResult, err := o.optimizer.Optimize(initialConfig, evaluateFunc)
	if err != nil {
		return nil, fmt.Errorf("optimization failed: %w", err)
	}

	// Build experiment result from optimization result
	result.BestConfig = optResult.BestConfig
	result.BestScore = optResult.BestScore
	result.Converged = optResult.Converged
	result.ConvergenceReason = optResult.ConvergenceReason
	result.Duration = time.Since(startTime)

	// Collect run contexts from optimization history
	o.mu.RLock()
	for _, step := range optResult.History {
		// Try to find the run context for this iteration
		runCtx := o.findRunContextForConfig(step.Config)
		if runCtx != nil {
			result.Runs = append(result.Runs, runCtx)
			result.TotalRuns++
			if runCtx.Status == RunStatusCompleted {
				result.CompletedRuns++
			} else if runCtx.Status == RunStatusFailed {
				result.FailedRuns++
			}
		}
	}
	o.mu.RUnlock()

	// Find the best run ID
	if result.BestConfig != nil {
		bestCtx := o.findRunContextForConfig(result.BestConfig)
		if bestCtx != nil {
			result.BestRunID = bestCtx.RunID
		}
	}

	return result, nil
}

// evaluateConfiguration runs a simulation for a given configuration and returns the objective score
func (o *Orchestrator) evaluateConfiguration(ctx context.Context, scenario *config.Scenario, durationMs int64) (float64, error) {
	// Convert scenario to YAML
	scenarioYAML, err := config.MarshalScenarioYAML(scenario)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal scenario: %w", err)
	}

	// Create run input
	runInput := &simulationv1.RunInput{
		ScenarioYaml: scenarioYAML,
		DurationMs:   durationMs,
		Seed:         0, // Use random seed for each run
	}

	// Generate run ID
	runID := fmt.Sprintf("opt-%d", time.Now().UnixNano())

	// Create run
	_, err = o.store.Create(runID, runInput)
	if err != nil {
		return 0, fmt.Errorf("failed to create run: %w", err)
	}

	// Create run context
	runCtx := &RunContext{
		RunID:     runID,
		Config:    scenario,
		Status:    RunStatusPending,
		CreatedAt: time.Now(),
	}

	o.mu.Lock()
	o.activeRuns[runID] = runCtx
	o.mu.Unlock()

	// Start the run
	_, err = o.executor.Start(runID)
	if err != nil {
		o.mu.Lock()
		runCtx.Status = RunStatusFailed
		runCtx.Error = err
		runCtx.CompletedAt = time.Now()
		o.mu.Unlock()
		return 0, fmt.Errorf("failed to start run: %w", err)
	}

	o.mu.Lock()
	runCtx.Status = RunStatusRunning
	o.mu.Unlock()

	// Wait for completion (with timeout)
	timeout := time.Duration(durationMs) * time.Millisecond
	if timeout < 30*time.Second {
		timeout = 30 * time.Second // Minimum timeout
	}
	timeout *= 2 // Allow some buffer

	completionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Poll for completion
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-completionCtx.Done():
			// Timeout or context cancelled
			o.mu.Lock()
			runCtx.Status = RunStatusFailed
			runCtx.Error = fmt.Errorf("run timed out or was cancelled")
			runCtx.CompletedAt = time.Now()
			o.mu.Unlock()
			return 0, runCtx.Error
		case <-ticker.C:
			// Check run status
			rec, ok := o.store.Get(runID)
			if !ok {
				o.mu.Lock()
				runCtx.Status = RunStatusFailed
				runCtx.Error = fmt.Errorf("run not found")
				runCtx.CompletedAt = time.Now()
				o.mu.Unlock()
				return 0, runCtx.Error
			}

			switch rec.Run.Status {
			case simulationv1.RunStatus_RUN_STATUS_COMPLETED:
				// Run completed successfully
				metrics := rec.Metrics
				if metrics == nil {
					o.mu.Lock()
					runCtx.Status = RunStatusFailed
					runCtx.Error = fmt.Errorf("run completed but no metrics available")
					runCtx.CompletedAt = time.Now()
					o.mu.Unlock()
					return 0, runCtx.Error
				}

				// Evaluate objective
				score, err := o.objective.Evaluate(metrics)
				if err != nil {
					o.mu.Lock()
					runCtx.Status = RunStatusFailed
					runCtx.Error = fmt.Errorf("failed to evaluate objective: %w", err)
					runCtx.CompletedAt = time.Now()
					o.mu.Unlock()
					return 0, runCtx.Error
				}

				o.mu.Lock()
				runCtx.Status = RunStatusCompleted
				runCtx.Score = score
				runCtx.Metrics = metrics
				runCtx.CompletedAt = time.Now()
				o.mu.Unlock()

				logger.Info("run completed", "run_id", runID, "score", score)
				return score, nil

			case simulationv1.RunStatus_RUN_STATUS_FAILED,
				simulationv1.RunStatus_RUN_STATUS_CANCELLED:
				// Run failed
				o.mu.Lock()
				runCtx.Status = RunStatusFailed
				runCtx.Error = fmt.Errorf("run failed: %s", rec.Run.Error)
				runCtx.CompletedAt = time.Now()
				o.mu.Unlock()
				return 0, runCtx.Error

			case simulationv1.RunStatus_RUN_STATUS_RUNNING,
				simulationv1.RunStatus_RUN_STATUS_PENDING:
				// Still running, continue waiting
				continue
			}
		}
	}
}

// findRunContextForConfig finds a run context that matches the given configuration
func (o *Orchestrator) findRunContextForConfig(scenario *config.Scenario) *RunContext {
	o.mu.RLock()
	defer o.mu.RUnlock()

	// Simple matching: find run with matching service replica counts
	for _, runCtx := range o.activeRuns {
		if runCtx.Config != nil && configsMatch(runCtx.Config, scenario) {
			return runCtx
		}
	}
	return nil
}

// configsMatch checks if two configurations match (simplified comparison)
func configsMatch(c1, c2 *config.Scenario) bool {
	if len(c1.Services) != len(c2.Services) {
		return false
	}
	for i := range c1.Services {
		if c1.Services[i].Replicas != c2.Services[i].Replicas {
			return false
		}
	}
	return true
}

// GetActiveRuns returns all currently active runs
func (o *Orchestrator) GetActiveRuns() []*RunContext {
	o.mu.RLock()
	defer o.mu.RUnlock()

	runs := make([]*RunContext, 0, len(o.activeRuns))
	for _, runCtx := range o.activeRuns {
		runs = append(runs, runCtx)
	}
	return runs
}

// GetRunContext returns the context for a specific run ID
func (o *Orchestrator) GetRunContext(runID string) (*RunContext, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	runCtx, ok := o.activeRuns[runID]
	return runCtx, ok
}
