package improvement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	store           *simd.RunStore
	executor        *simd.RunExecutor
	optimizer       *Optimizer
	objective       ObjectiveFunction
	mu              sync.RWMutex
	activeRuns      map[string]*RunContext
	maxParallelRuns int // Maximum number of parallel runs
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
		store:           store,
		executor:        executor,
		optimizer:       optimizer,
		objective:       objective,
		activeRuns:      make(map[string]*RunContext),
		maxParallelRuns: 1, // Default to sequential execution
	}
}

// WithMaxParallelRuns sets the maximum number of parallel runs
func (o *Orchestrator) WithMaxParallelRuns(max int) *Orchestrator {
	if max < 1 {
		max = 1
	}
	o.maxParallelRuns = max
	return o
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
	// This function can be called in parallel by the optimizer
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

	// Generate unique run ID using cryptographic random bytes for parallel execution safety
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return 0, fmt.Errorf("failed to generate run ID: %w", err)
	}
	runID := fmt.Sprintf("opt-%d-%s", time.Now().UnixNano(), hex.EncodeToString(randomBytes))

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
	// For very short durations (likely tests), use a more reasonable minimum
	if durationMs < 5000 {
		// For test durations (< 5s), allow up to 10 seconds
		if timeout < 10*time.Second {
			timeout = 10 * time.Second
		}
	} else {
		// For production durations, use longer timeout
		if timeout < 30*time.Second {
			timeout = 30 * time.Second // Minimum timeout
		}
		timeout *= 2 // Allow some buffer for longer runs
	}

	completionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Poll for completion - use faster polling for short durations (tests)
	pollInterval := 500 * time.Millisecond
	if durationMs < 5000 {
		pollInterval = 100 * time.Millisecond // Faster polling for tests
	}
	ticker := time.NewTicker(pollInterval)
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

// configsMatch checks if two configurations match by comparing all relevant parameters
func configsMatch(c1, c2 *config.Scenario) bool {
	if c1 == nil || c2 == nil {
		return c1 == c2
	}

	// Compare services
	if len(c1.Services) != len(c2.Services) {
		return false
	}
	for i := range c1.Services {
		s1, s2 := &c1.Services[i], &c2.Services[i]
		if s1.ID != s2.ID || s1.Replicas != s2.Replicas ||
			s1.CPUCores != s2.CPUCores || s1.MemoryMB != s2.MemoryMB ||
			s1.Model != s2.Model {
			return false
		}
	}

	// Compare workload patterns
	if len(c1.Workload) != len(c2.Workload) {
		return false
	}
	for i := range c1.Workload {
		w1, w2 := &c1.Workload[i], &c2.Workload[i]
		if w1.From != w2.From || w1.To != w2.To ||
			w1.Arrival.Type != w2.Arrival.Type ||
			w1.Arrival.RateRPS != w2.Arrival.RateRPS {
			return false
		}
	}

	// Compare policies if present
	if (c1.Policies == nil) != (c2.Policies == nil) {
		return false
	}
	if c1.Policies != nil && c2.Policies != nil {
		// Compare autoscaling policy
		if (c1.Policies.Autoscaling == nil) != (c2.Policies.Autoscaling == nil) {
			return false
		}
		if c1.Policies.Autoscaling != nil && c2.Policies.Autoscaling != nil {
			a1, a2 := c1.Policies.Autoscaling, c2.Policies.Autoscaling
			if a1.Enabled != a2.Enabled || a1.TargetCPUUtil != a2.TargetCPUUtil ||
				a1.ScaleStep != a2.ScaleStep {
				return false
			}
		}

		// Compare retry policy
		if (c1.Policies.Retries == nil) != (c2.Policies.Retries == nil) {
			return false
		}
		if c1.Policies.Retries != nil && c2.Policies.Retries != nil {
			r1, r2 := c1.Policies.Retries, c2.Policies.Retries
			if r1.Enabled != r2.Enabled || r1.MaxRetries != r2.MaxRetries ||
				r1.Backoff != r2.Backoff || r1.BaseMs != r2.BaseMs {
				return false
			}
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
