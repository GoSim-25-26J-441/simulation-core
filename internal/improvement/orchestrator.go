package improvement

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
)

// Orchestrator manages multi-run optimization experiments
type Orchestrator struct {
	store            *simd.RunStore
	executor         *simd.RunExecutor
	optimizer        *Optimizer
	objective        ObjectiveFunction
	mu               sync.RWMutex
	activeRuns       map[string]*RunContext
	maxParallelRuns  int // Maximum number of parallel runs
	safety           OptimizationSafetyConfig
	candidateSem     chan struct{}
	failedCandidates int32

	// batchCandidateStore is set only during RunBatchExperiment for hash→runID resolution.
	batchCandMu         sync.Mutex
	batchCandidateStore *CandidateStore
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
	Outcome     CandidateOutcome
	Reason      string
	Duration    time.Duration
	HasMetrics  bool
}

type CandidateOutcome string

const (
	CandidateSucceeded     CandidateOutcome = "succeeded"
	CandidateFailed        CandidateOutcome = "failed"
	CandidateTimedOut      CandidateOutcome = "timed_out"
	CandidateCancelled     CandidateOutcome = "cancelled"
	CandidateLimitExceeded CandidateOutcome = "limit_exceeded"
	CandidateInvalidConfig CandidateOutcome = "invalid_config"
)

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
	BestConfig *config.Scenario
	// BestScore for legacy hill-climb is the objective value; for batch runs it is efficiency-only.
	// Use Batch and BatchCandidateRunIDs (or Run batch_* protobuf fields) for full batch semantics.
	BestScore         float64
	BestRunID         string
	Iterations        int
	TotalRuns         int
	CompletedRuns     int
	FailedRuns        int
	Runs              []*RunContext
	Duration          time.Duration
	Converged         bool
	ConvergenceReason string
	// Batch is set when batch beam search was used (RunBatchExperiment).
	Batch *BatchSearchResult
	// BatchCandidateRunIDs lists candidate runs in CompareBatchScores order (batch runs only).
	BatchCandidateRunIDs []string
}

// NewOrchestrator creates a new optimization orchestrator
func NewOrchestrator(store *simd.RunStore, executor *simd.RunExecutor, optimizer *Optimizer, objective ObjectiveFunction) *Orchestrator {
	safety := OptimizationSafetyConfigFromEnv()
	return &Orchestrator{
		store:           store,
		executor:        executor,
		optimizer:       optimizer,
		objective:       objective,
		activeRuns:      make(map[string]*RunContext),
		maxParallelRuns: 1,
		safety:          safety,
		candidateSem:    make(chan struct{}, safety.MaxConcurrentCandidates),
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
	if o.safety.MaxWallClockRuntime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.safety.MaxWallClockRuntime)
		defer cancel()
	}
	atomic.StoreInt32(&o.failedCandidates, 0)
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
	result.Iterations = optResult.Iterations
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
	metrics, err := o.evaluateConfigurationMetrics(ctx, scenario, durationMs, nil, 0)
	if err != nil {
		return 0, err
	}
	return o.evaluateRunScore(scenario, metrics)
}

// evaluateConfigurationMetrics runs one candidate simulation and returns metrics. When cand is non-nil,
// registers ConfigHash(scenario)→runID for lookup. seed is passed to RunInput when > 0 (deterministic re-runs).
func (o *Orchestrator) evaluateConfigurationMetrics(ctx context.Context, scenario *config.Scenario, durationMs int64, cand *CandidateStore, seed int64) (*simulationv1.RunMetrics, error) {
	if int(atomic.LoadInt32(&o.failedCandidates)) >= o.safety.MaxFailedCandidates {
		return nil, fmt.Errorf("max failed candidates reached")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case o.candidateSem <- struct{}{}:
	}
	defer func() { <-o.candidateSem }()
	if durationMs <= 0 {
		durationMs = o.safety.DefaultEvaluationDuration
	}
	candidateCtx := ctx
	var cancel context.CancelFunc
	if o.safety.CandidateMaxWallClock > 0 {
		candidateCtx, cancel = context.WithTimeout(ctx, o.safety.CandidateMaxWallClock)
		defer cancel()
	}
	started := time.Now()
	scenarioYAML, err := config.MarshalScenarioYAML(scenario)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal scenario: %w", err)
	}

	runInput := &simulationv1.RunInput{
		ScenarioYaml: scenarioYAML,
		DurationMs:   durationMs,
		Seed:         seed,
	}

	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		return nil, fmt.Errorf("failed to generate run ID: %w", err)
	}
	runID := fmt.Sprintf("opt-%d-%s", time.Now().UnixNano(), hex.EncodeToString(randomBytes))

	_, err = o.store.Create(runID, runInput)
	if err != nil {
		return nil, fmt.Errorf("failed to create run: %w", err)
	}

	if cand != nil {
		cand.Register(batchspec.ConfigHash(scenario), runID)
	}

	runCtx := &RunContext{
		RunID:     runID,
		Config:    scenario,
		Status:    RunStatusPending,
		CreatedAt: time.Now(),
	}

	o.mu.Lock()
	o.activeRuns[runID] = runCtx
	o.mu.Unlock()

	logger.Info("candidate started", "parent_context", "optimization", "candidate_run_id", runID)
	_, err = o.executor.Start(runID)
	if err != nil {
		o.mu.Lock()
		runCtx.Status = RunStatusFailed
		runCtx.Error = err
		runCtx.Outcome = CandidateFailed
		runCtx.Reason = err.Error()
		runCtx.Duration = time.Since(started)
		runCtx.CompletedAt = time.Now()
		o.mu.Unlock()
		atomic.AddInt32(&o.failedCandidates, 1)
		return nil, fmt.Errorf("failed to start run: %w", err)
	}

	o.mu.Lock()
	runCtx.Status = RunStatusRunning
	o.mu.Unlock()

	timeout := time.Duration(durationMs) * time.Millisecond
	if durationMs < 5000 {
		if timeout < 10*time.Second {
			timeout = 10 * time.Second
		}
	} else {
		if timeout < 30*time.Second {
			timeout = 30 * time.Second
		}
		timeout *= 2
	}

	completionCtx, cancel := context.WithTimeout(candidateCtx, timeout)
	defer cancel()

	pollInterval := 500 * time.Millisecond
	if durationMs < 5000 {
		pollInterval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-completionCtx.Done():
			if o.executor != nil {
				if _, stopErr := o.executor.Stop(runID); stopErr != nil {
					logger.Debug("orchestrator timeout: Stop candidate run", "run_id", runID, "error", stopErr)
				}
			}
			o.mu.Lock()
			runCtx.Status = RunStatusFailed
			switch {
			case errors.Is(completionCtx.Err(), context.DeadlineExceeded):
				runCtx.Error = completionCtx.Err()
				runCtx.Outcome = CandidateTimedOut
				runCtx.Reason = "candidate timeout"
			case completionCtx.Err() != nil:
				runCtx.Error = completionCtx.Err()
				runCtx.Outcome = CandidateCancelled
				runCtx.Reason = completionCtx.Err().Error()
			default:
				runCtx.Error = fmt.Errorf("run timed out or was cancelled")
				runCtx.Outcome = CandidateFailed
				runCtx.Reason = runCtx.Error.Error()
			}
			runCtx.Duration = time.Since(started)
			runCtx.CompletedAt = time.Now()
			o.mu.Unlock()
			atomic.AddInt32(&o.failedCandidates, 1)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, runCtx.Error
		case <-ticker.C:
			rec, ok := o.store.Get(runID)
			if !ok {
				o.mu.Lock()
				runCtx.Status = RunStatusFailed
				runCtx.Error = fmt.Errorf("run not found")
				runCtx.CompletedAt = time.Now()
				o.mu.Unlock()
				return nil, runCtx.Error
			}

			switch rec.Run.Status {
			case simulationv1.RunStatus_RUN_STATUS_COMPLETED:
				metrics := rec.Metrics
				if metrics == nil {
					o.mu.Lock()
					runCtx.Status = RunStatusFailed
					runCtx.Error = fmt.Errorf("run completed but no metrics available")
					runCtx.Outcome = CandidateFailed
					runCtx.Reason = runCtx.Error.Error()
					runCtx.Duration = time.Since(started)
					runCtx.CompletedAt = time.Now()
					o.mu.Unlock()
					atomic.AddInt32(&o.failedCandidates, 1)
					return nil, runCtx.Error
				}
				score, evalErr := o.evaluateRunScore(scenario, metrics)
				if evalErr != nil {
					o.mu.Lock()
					runCtx.Status = RunStatusFailed
					runCtx.Error = fmt.Errorf("failed to evaluate objective: %w", evalErr)
					runCtx.Outcome = CandidateFailed
					runCtx.Reason = runCtx.Error.Error()
					runCtx.Duration = time.Since(started)
					runCtx.CompletedAt = time.Now()
					o.mu.Unlock()
					atomic.AddInt32(&o.failedCandidates, 1)
					return nil, evalErr
				}
				o.mu.Lock()
				runCtx.Status = RunStatusCompleted
				runCtx.Score = score
				runCtx.Metrics = metrics
				runCtx.Outcome = CandidateSucceeded
				runCtx.Reason = ""
				runCtx.Duration = time.Since(started)
				runCtx.HasMetrics = true
				runCtx.CompletedAt = time.Now()
				o.mu.Unlock()

				logger.Info("run completed", "run_id", runID, "score", score)
				return metrics, nil

			case simulationv1.RunStatus_RUN_STATUS_FAILED,
				simulationv1.RunStatus_RUN_STATUS_CANCELLED,
				simulationv1.RunStatus_RUN_STATUS_STOPPED:
				o.mu.Lock()
				runCtx.Status = RunStatusFailed
				runCtx.Error = fmt.Errorf("run failed: %s", rec.Run.Error)
				runCtx.Duration = time.Since(started)
				runCtx.Reason = rec.Run.Error
				if strings.Contains(strings.ToLower(rec.Run.Error), "limit") {
					runCtx.Outcome = CandidateLimitExceeded
				} else {
					runCtx.Outcome = CandidateFailed
				}
				runCtx.CompletedAt = time.Now()
				o.mu.Unlock()
				atomic.AddInt32(&o.failedCandidates, 1)
				return nil, runCtx.Error

			case simulationv1.RunStatus_RUN_STATUS_RUNNING,
				simulationv1.RunStatus_RUN_STATUS_PENDING:
				continue
			}
		}
	}
}

func (o *Orchestrator) evaluateRunScore(scenario *config.Scenario, metrics *simulationv1.RunMetrics) (float64, error) {
	if o.objective == nil {
		return 0, fmt.Errorf("objective function is nil")
	}
	if o.objective.Name() == string(ObjectiveMinimizeCost) {
		return EvaluateInfrastructureCost(scenario), nil
	}
	return o.objective.Evaluate(metrics)
}

// findRunContextForConfig finds a run context that matches the given configuration
func (o *Orchestrator) findRunContextForConfig(scenario *config.Scenario) *RunContext {
	o.batchCandMu.Lock()
	store := o.batchCandidateStore
	o.batchCandMu.Unlock()

	if store != nil {
		if rid, ok := store.Lookup(batchspec.ConfigHash(scenario)); ok {
			o.mu.RLock()
			rc := o.activeRuns[rid]
			o.mu.RUnlock()
			if rc != nil {
				return rc
			}
		}
	}

	o.mu.RLock()
	defer o.mu.RUnlock()

	for _, runCtx := range o.activeRuns {
		if runCtx.Config != nil && configsMatch(runCtx.Config, scenario) {
			return runCtx
		}
	}
	return nil
}

// configsMatch reports whether two scenarios are identical for optimizer identity and runtime
// semantics. It delegates to batchspec.ConfigHash so there is a single definition of
// "same scenario" for deduplication, candidate lookup, and seed derivation.
func configsMatch(c1, c2 *config.Scenario) bool {
	return batchspec.ScenarioSemanticsEqual(c1, c2)
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
