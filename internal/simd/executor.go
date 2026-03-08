package simd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
	"google.golang.org/protobuf/proto"
)

// RunExecutor manages asynchronous run execution and per-run cancellation.
type RunExecutor struct {
	store    *RunStore
	notifier *Notifier

	optimizationRunner OptimizationRunner // optional; when set, optimization runs use it

	mu               sync.Mutex
	cancels          map[string]context.CancelFunc
	workloadStates   map[string]*WorkloadState    // key: runID
	resourceManagers map[string]*resource.Manager // key: runID; for dynamic replica updates
	policyManagers   map[string]*policy.Manager   // key: runID; for dynamic policy updates
}

// SetOptimizationRunner sets the optimization runner for multi-run experiments.
// Must be called before starting optimization runs.
func (e *RunExecutor) SetOptimizationRunner(r OptimizationRunner) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.optimizationRunner = r
}

var (
	ErrRunNotFound  = errors.New("run not found")
	ErrRunTerminal  = errors.New("run is terminal")
	ErrRunIDMissing = errors.New("run_id is required")
)

func NewRunExecutor(store *RunStore) *RunExecutor {
	return &RunExecutor{
		store:            store,
		notifier:         NewNotifier(),
		cancels:          make(map[string]context.CancelFunc),
		workloadStates:   make(map[string]*WorkloadState),
		resourceManagers: make(map[string]*resource.Manager),
		policyManagers:   make(map[string]*policy.Manager),
	}
}

// Start begins executing a run asynchronously.
// Returns the updated run state (RUNNING) or an error.
func (e *RunExecutor) Start(runID string) (*RunRecord, error) {
	if runID == "" {
		return nil, ErrRunIDMissing
	}

	rec, ok := e.store.Get(runID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	switch rec.Run.Status {
	case simulationv1.RunStatus_RUN_STATUS_RUNNING:
		return rec, nil
	case simulationv1.RunStatus_RUN_STATUS_COMPLETED,
		simulationv1.RunStatus_RUN_STATUS_FAILED,
		simulationv1.RunStatus_RUN_STATUS_CANCELLED,
		simulationv1.RunStatus_RUN_STATUS_STOPPED:
		return nil, fmt.Errorf("%w: %s", ErrRunTerminal, runID)
	}

	updated, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_RUNNING, "")
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	// Replace any existing cancel func (shouldn't happen for non-running, but safe).
	if old, exists := e.cancels[runID]; exists {
		old()
	}
	e.cancels[runID] = cancel
	e.mu.Unlock()

	// Optimization runs can use either the batch optimizer (multi-run) or the
	// online controller mode, which adjusts configuration within a single long-
	// running simulation.
	if opt := rec.Input.Optimization; opt != nil {
		if opt.Online {
			go e.runOnlineOptimization(ctx, runID)
		} else {
			go e.runOptimization(ctx, runID)
		}
	} else {
		go e.runSimulation(ctx, runID)
	}
	return updated, nil
}

// Stop requests cancellation for a running run and marks it stopped.
func (e *RunExecutor) Stop(runID string) (*RunRecord, error) {
	if runID == "" {
		return nil, ErrRunIDMissing
	}

	e.mu.Lock()
	cancel, ok := e.cancels[runID]
	e.mu.Unlock()

	if ok {
		cancel()
	}

	updated, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_STOPPED, "")
	if err != nil {
		return nil, err
	}
	// For online optimization runs, skip notification here: runOnlineOptimization will
	// finalize metrics and send a single callback with metrics. Sending here would
	// cause a duplicate callback with empty metrics.
	isOnlineOpt := updated.Input != nil && updated.Input.Optimization != nil && updated.Input.Optimization.Online
	if !isOnlineOpt {
		e.sendNotificationIfConfigured(updated)
	}
	return updated, nil
}

func (e *RunExecutor) cleanup(runID string) {
	e.mu.Lock()
	if cancel, ok := e.cancels[runID]; ok {
		// Ensure cancel is called and remove.
		cancel()
		delete(e.cancels, runID)
	}
	// Stop and remove workload state, resource manager, and policy manager
	if ws, ok := e.workloadStates[runID]; ok {
		ws.Stop()
		delete(e.workloadStates, runID)
	}
	delete(e.resourceManagers, runID)
	delete(e.policyManagers, runID)
	e.mu.Unlock()
}

// getCallbackSecret extracts the callback secret from a run record, returning empty string if not set
func getCallbackSecret(rec *RunRecord) string {
	if rec == nil || rec.Input == nil {
		return ""
	}
	return rec.Input.CallbackSecret
}

// sendNotificationIfConfigured sends a notification to the callback URL if configured in the run record
func (e *RunExecutor) sendNotificationIfConfigured(rec *RunRecord) {
	if rec == nil || rec.Input == nil || rec.Input.CallbackUrl == "" {
		return
	}

	e.notifier.Notify(rec.Input.CallbackUrl, getCallbackSecret(rec), rec)
}

func (e *RunExecutor) runOptimization(ctx context.Context, runID string) {
	defer e.cleanup(runID)

	rec, ok := e.store.Get(runID)
	if !ok {
		logger.Error("run not found", "run_id", runID)
		return
	}

	e.mu.Lock()
	runner := e.optimizationRunner
	e.mu.Unlock()

	if runner == nil {
		logger.Error("optimization runner not configured", "run_id", runID)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, "optimization not enabled"); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	scenario, err := config.ParseScenarioYAMLString(rec.Input.ScenarioYaml)
	if err != nil {
		logger.Error("failed to parse scenario YAML", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("invalid scenario: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	opt := rec.Input.Optimization
	params := &OptimizationParams{
		Objective:     "p95_latency_ms",
		MaxIterations: 10,
		StepSize:      1.0,
	}
	if opt != nil {
		if opt.Objective != "" {
			params.Objective = opt.Objective
		}
		if opt.MaxIterations > 0 {
			params.MaxIterations = opt.MaxIterations
		}
		if opt.StepSize > 0 {
			params.StepSize = opt.StepSize
		}
	}

	// Determine evaluation duration for each candidate run in the optimization.
	// Priority:
	// 1) Explicit RunInput.DurationMs (per-run override)
	// 2) OptimizationConfig.EvaluationDurationMs (per-experiment default)
	// 3) Built-in default (10s) for backwards compatibility
	durationMs := rec.Input.DurationMs
	if durationMs <= 0 && opt != nil && opt.EvaluationDurationMs > 0 {
		durationMs = opt.EvaluationDurationMs
	}
	if durationMs <= 0 {
		durationMs = 10000 // 10 seconds default
	}

	logger.Info("starting optimization run", "run_id", runID, "objective", params.Objective, "max_iterations", params.MaxIterations)

	bestRunID, bestScore, iterations, candidateRunIDs, err := runner.RunExperiment(ctx, runID, scenario, durationMs, params)
	if err != nil {
		if ctx.Err() != nil {
			logger.Info("optimization cancelled", "run_id", runID)
			return
		}
		logger.Error("optimization failed", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, err.Error()); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	if err := e.store.SetOptimizationResult(runID, bestRunID, bestScore, iterations, candidateRunIDs); err != nil {
		logger.Error("failed to set optimization result", "run_id", runID, "error", err)
	}

	// Copy the best run's metrics onto the parent optimization run so GET /metrics
	// and SSE metrics_snapshot (on the next tick before complete) expose them.
	if bestRunID != "" {
		if bestRec, ok := e.store.Get(bestRunID); ok && bestRec.Metrics != nil {
			if setErr := e.store.SetMetrics(runID, bestRec.Metrics); setErr != nil {
				logger.Error("failed to set parent run metrics from best run", "run_id", runID, "best_run_id", bestRunID, "error", setErr)
			}
		}
	}

	updated, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_COMPLETED, "")
	if err != nil {
		logger.Error("failed to set completed status", "run_id", runID, "error", err)
	} else {
		logger.Info("optimization completed", "run_id", runID,
			"best_run_id", bestRunID, "best_score", bestScore, "iterations", iterations)
		e.sendNotificationIfConfigured(updated)
	}
}

// runOnlineOptimization runs an "online" optimization experiment inside a single
// long-lived simulation run. It reuses the standard simulation pipeline but
// adds a controller loop that periodically inspects metrics and adjusts the
// configuration (e.g. replicas) using the existing dynamic configuration APIs.
func (e *RunExecutor) runOnlineOptimization(ctx context.Context, runID string) {
	defer e.cleanup(runID)

	rec, ok := e.store.Get(runID)
	if !ok {
		logger.Error("run not found", "run_id", runID)
		return
	}

	if rec.Input == nil || rec.Input.Optimization == nil {
		logger.Error("online optimization requested without optimization config", "run_id", runID)
		return
	}
	opt := rec.Input.Optimization

	// Parse scenario YAML
	scenario, err := config.ParseScenarioYAMLString(rec.Input.ScenarioYaml)
	if err != nil {
		logger.Error("failed to parse scenario YAML", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("invalid scenario: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	// Use a very long duration; the run is expected to be stopped explicitly.
	const onlineRunDuration = 365 * 24 * time.Hour

	// Create engine
	eng := engine.NewEngine(runID)

	// Enable real-time mode if requested
	if rec.Input.RealTimeMode {
		eng.SetRealTimeMode(true)
		logger.Info("real-time mode enabled (online)", "run_id", runID)
	}

	// Wire cancellation: when context is cancelled, stop the engine
	go func() {
		<-ctx.Done()
		eng.Stop()
	}()

	// Initialize resource manager from scenario
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		logger.Error("failed to initialize resource manager", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("resource initialization failed: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	// Initialize metrics collector
	metricsCollector := metrics.NewCollector()
	metricsCollector.Start()

	// Store collector reference for later access
	if err := e.store.SetCollector(runID, metricsCollector); err != nil {
		logger.Error("failed to store collector", "run_id", runID, "error", err)
	}

	// Initialize policy manager from scenario
	var policies *policy.Manager
	if scenario.Policies != nil {
		configPolicies := &config.Policies{
			Autoscaling: scenario.Policies.Autoscaling,
			Retries:     scenario.Policies.Retries,
		}
		policies = policy.NewPolicyManager(configPolicies)
	} else {
		policies = policy.NewPolicyManager(nil)
	}

	// Create scenario state and register handlers
	state, err := newScenarioState(scenario, rm, metricsCollector, policies)
	if err != nil {
		logger.Error("failed to create scenario state", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("scenario state creation failed: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}
	RegisterHandlers(eng, state)

	// Initialize workload state for continuous event generation
	startTime := eng.GetSimTime()
	endTime := startTime.Add(onlineRunDuration)
	workloadState := NewWorkloadState(runID, eng, endTime)
	if err := workloadState.Start(scenario, startTime); err != nil {
		logger.Error("failed to start workload state", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("workload state initialization failed: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	// Store workload state, resource manager, and policy manager for dynamic updates
	e.mu.Lock()
	e.workloadStates[runID] = workloadState
	e.resourceManagers[runID] = rm
	e.policyManagers[runID] = policies
	e.mu.Unlock()

	// Start the online controller loop
	go e.runOnlineController(ctx, runID, scenario, metricsCollector, opt, rm)

	// Run simulation; expect it to be stopped explicitly via StopRun.
	logger.Info("starting online optimization run", "run_id", runID, "duration", onlineRunDuration)
	if err := eng.Run(onlineRunDuration); err != nil {
		// If cancelled, handle based on current run status.
		if ctx.Err() != nil {
			rec, ok := e.store.Get(runID)
			if !ok {
				logger.Info("online simulation cancelled; run record not found", "run_id", runID)
				return
			}

			// If the run was explicitly stopped (STOPPED status), finalize metrics
			// similarly to the natural completion path so callbacks and GET /metrics
			// have a final aggregated snapshot.
			if rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_STOPPED {
				logger.Info("online simulation stopped; finalizing metrics", "run_id", runID)

				metricsCollector.Stop()

				serviceLabels := make([]map[string]string, 0, len(scenario.Services))
				for _, svc := range scenario.Services {
					serviceLabels = append(serviceLabels, metrics.CreateServiceLabels(svc.ID))
				}
				engineMetrics := metrics.ConvertToRunMetrics(metricsCollector, serviceLabels)
				for _, svc := range scenario.Services {
					if sm := engineMetrics.ServiceMetrics[svc.ID]; sm != nil {
						sm.ActiveReplicas = svc.Replicas
					}
				}

				pbMetrics := convertMetricsToProto(engineMetrics)
				if err := e.store.SetMetrics(runID, pbMetrics); err != nil {
					logger.Error("failed to set metrics for stopped online run", "run_id", runID, "error", err)
				}

				// Set optimization result so callback includes best_run_id and top_candidates (self).
				n := len(rec.OptimizationHistory)
				steps := int32(n)
				if n > math.MaxInt32 {
					steps = math.MaxInt32
				}
				if err := e.store.SetOptimizationResult(runID, runID, 0, steps, []string{runID}); err != nil {
					logger.Error("failed to set optimization result for stopped online run", "run_id", runID, "error", err)
				}

				// Fetch updated record (with metrics) for notification.
				if updatedRec, ok := e.store.Get(runID); ok {
					e.sendNotificationIfConfigured(updatedRec)
				}
				return
			}

			// For other cancellation reasons, keep legacy behaviour (no aggregated metrics).
			logger.Info("online simulation cancelled", "run_id", runID)
			e.sendNotificationIfConfigured(rec)
			return
		}
		logger.Error("online simulation failed", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, err.Error()); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	// If the engine naturally reaches the (very long) end time, finalize metrics as in runSimulation.
	finalSimTime := eng.GetSimTime()
	simDuration := finalSimTime.Sub(startTime)
	logger.Info("online simulation completed", "run_id", runID,
		"simulation_duration", simDuration,
		"expected_duration", onlineRunDuration)

	metricsCollector.Stop()

	serviceLabels := make([]map[string]string, 0, len(scenario.Services))
	for _, svc := range scenario.Services {
		serviceLabels = append(serviceLabels, metrics.CreateServiceLabels(svc.ID))
	}
	engineMetrics := metrics.ConvertToRunMetrics(metricsCollector, serviceLabels)
	for _, svc := range scenario.Services {
		if sm := engineMetrics.ServiceMetrics[svc.ID]; sm != nil {
			sm.ActiveReplicas = svc.Replicas
		}
	}

	pbMetrics := convertMetricsToProto(engineMetrics)
	if err := e.store.SetMetrics(runID, pbMetrics); err != nil {
		logger.Error("failed to set metrics", "run_id", runID, "error", err)
	}

	// Mark as completed if still running
	rec, ok = e.store.Get(runID)
	if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_RUNNING {
		// Set optimization result so callback includes best_run_id and top_candidates (self).
		n := len(rec.OptimizationHistory)
		steps := int32(n)
		if n > math.MaxInt32 {
			steps = math.MaxInt32
		}
		if err := e.store.SetOptimizationResult(runID, runID, 0, steps, []string{runID}); err != nil {
			logger.Error("failed to set optimization result for online run", "run_id", runID, "error", err)
		}
		if updated, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_COMPLETED, ""); err != nil {
			logger.Error("failed to set completed status", "run_id", runID, "error", err)
		} else {
			logger.Info("online optimization run completed", "run_id", runID)
			e.sendNotificationIfConfigured(updated)
		}
	}
}

func (e *RunExecutor) runSimulation(ctx context.Context, runID string) {
	defer e.cleanup(runID)

	// Get run record
	rec, ok := e.store.Get(runID)
	if !ok {
		logger.Error("run not found", "run_id", runID)
		return
	}

	// Parse scenario YAML
	scenario, err := config.ParseScenarioYAMLString(rec.Input.ScenarioYaml)
	if err != nil {
		logger.Error("failed to parse scenario YAML", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("invalid scenario: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	// Determine simulation duration
	duration := time.Duration(rec.Input.DurationMs) * time.Millisecond
	if duration <= 0 {
		// Default duration if not specified
		duration = 10 * time.Second
	}

	// Create engine
	eng := engine.NewEngine(runID)

	// Enable real-time mode if requested (for real-time dashboards/monitoring)
	if rec.Input.RealTimeMode {
		eng.SetRealTimeMode(true)
		logger.Info("real-time mode enabled", "run_id", runID)
	}

	// Wire cancellation: when context is cancelled, stop the engine
	go func() {
		<-ctx.Done()
		eng.Stop()
	}()

	// Initialize resource manager from scenario
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		logger.Error("failed to initialize resource manager", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("resource initialization failed: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	// Initialize metrics collector
	metricsCollector := metrics.NewCollector()
	metricsCollector.Start()

	// Store collector reference for later access
	if err := e.store.SetCollector(runID, metricsCollector); err != nil {
		logger.Error("failed to store collector", "run_id", runID, "error", err)
		// Continue anyway, as this is not critical for simulation execution
	}

	// Initialize policy manager from scenario
	var policies *policy.Manager
	if scenario.Policies != nil {
		// Convert scenario.Policies to config.Policies for PolicyManager
		configPolicies := &config.Policies{
			Autoscaling: scenario.Policies.Autoscaling,
			Retries:     scenario.Policies.Retries,
		}
		policies = policy.NewPolicyManager(configPolicies)
	} else {
		policies = policy.NewPolicyManager(nil)
	}

	// Create scenario state and register handlers
	state, err := newScenarioState(scenario, rm, metricsCollector, policies)
	if err != nil {
		logger.Error("failed to create scenario state", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("scenario state creation failed: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}
	RegisterHandlers(eng, state)

	// Initialize workload state for continuous event generation
	startTime := eng.GetSimTime()
	endTime := startTime.Add(duration)
	workloadState := NewWorkloadState(runID, eng, endTime)
	if err := workloadState.Start(scenario, startTime); err != nil {
		logger.Error("failed to start workload state", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("workload state initialization failed: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	// Store workload state, resource manager, and policy manager for dynamic updates
	e.mu.Lock()
	e.workloadStates[runID] = workloadState
	e.resourceManagers[runID] = rm
	e.policyManagers[runID] = policies
	e.mu.Unlock()

	// Run simulation
	logger.Info("starting simulation", "run_id", runID, "duration", duration)
	if err := eng.Run(duration); err != nil {
		// Check if it was cancelled
		if ctx.Err() != nil {
			logger.Info("simulation cancelled", "run_id", runID)
			rec, _ := e.store.Get(runID)
			e.sendNotificationIfConfigured(rec)
			return
		}
		logger.Error("simulation failed", "run_id", runID, "error", err)
		if updated, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, err.Error()); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		} else {
			e.sendNotificationIfConfigured(updated)
		}
		return
	}

	// Get final simulation time to calculate actual simulation duration
	finalSimTime := eng.GetSimTime()
	simDuration := finalSimTime.Sub(startTime)
	logger.Info("simulation completed", "run_id", runID,
		"simulation_duration", simDuration,
		"expected_duration", duration)

	// Stop metrics collection
	metricsCollector.Stop()

	// Build service labels for metrics conversion
	serviceLabels := make([]map[string]string, 0)
	for _, svc := range scenario.Services {
		serviceLabels = append(serviceLabels, metrics.CreateServiceLabels(svc.ID))
	}

	// Convert metrics collector data to RunMetrics
	engineMetrics := metrics.ConvertToRunMetrics(metricsCollector, serviceLabels)

	// Populate ActiveReplicas from scenario (not recorded in collector)
	for _, svc := range scenario.Services {
		if sm := engineMetrics.ServiceMetrics[svc.ID]; sm != nil {
			sm.ActiveReplicas = svc.Replicas
		}
	}

	// Convert engine metrics to protobuf format
	pbMetrics := convertMetricsToProto(engineMetrics)

	// Store metrics
	if err := e.store.SetMetrics(runID, pbMetrics); err != nil {
		logger.Error("failed to set metrics", "run_id", runID, "error", err)
	}

	// Mark as completed if still running
	rec, ok = e.store.Get(runID)
	if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_RUNNING {
		if updated, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_COMPLETED, ""); err != nil {
			logger.Error("failed to set completed status", "run_id", runID, "error", err)
		} else {
			logger.Info("run completed", "run_id", runID,
				"total_requests", pbMetrics.TotalRequests,
				"throughput_rps", pbMetrics.ThroughputRps)
			e.sendNotificationIfConfigured(updated)
		}
	}
}

// recordOptimizationStep appends an optimization step to the run's history for backend persistence.
func (e *RunExecutor) recordOptimizationStep(runID string, iterationIndex int32, targetP95, scoreP95 float64, reason string, prevConfig, currConfig *simulationv1.RunConfiguration) {
	if prevConfig == nil || currConfig == nil {
		return
	}
	step := &simulationv1.OptimizationStep{
		IterationIndex: iterationIndex,
		TargetP95Ms:    targetP95,
		ScoreP95Ms:     scoreP95,
		Reason:         reason,
		PreviousConfig: proto.Clone(prevConfig).(*simulationv1.RunConfiguration),
		CurrentConfig:  proto.Clone(currConfig).(*simulationv1.RunConfiguration),
	}
	if err := e.store.AppendOptimizationStep(runID, step); err != nil {
		logger.Error("failed to append optimization step", "run_id", runID, "error", err)
	}
}

// runOnlineController implements a simple online controller that periodically inspects
// metrics and adjusts configuration (currently service replicas) to keep p95 latency
// near the configured target. It uses the existing dynamic configuration APIs via the
// executor's resource manager map.
func (e *RunExecutor) runOnlineController(
	ctx context.Context,
	runID string,
	scenario *config.Scenario,
	collector *metrics.Collector,
	opt *simulationv1.OptimizationConfig,
	rm *resource.Manager,
) {
	if opt == nil {
		return
	}

	targetP95 := opt.TargetP95LatencyMs
	if targetP95 <= 0 {
		// No target specified; nothing to control.
		return
	}

	interval := time.Second
	if opt.ControlIntervalMs > 0 {
		interval = time.Duration(opt.ControlIntervalMs) * time.Millisecond
	}

	// Precompute service labels for metrics conversion.
	serviceLabels := make([]map[string]string, 0, len(scenario.Services))
	for _, svc := range scenario.Services {
		serviceLabels = append(serviceLabels, metrics.CreateServiceLabels(svc.ID))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	bestScore := math.Inf(1)
	var iter int32
	var stepIndex int32

	const (
		cpuHighThreshold     = 0.8 // above this, consider service CPU "hot"
		hostCPUHighThreshold = 0.8 // above this, consider host CPU "hot"
	)

	// Host scaling bounds. Defaults: use the initial scenario host count as both
	// the minimum and maximum when not explicitly configured.
	initialHosts := len(scenario.Hosts)
	minHosts := int(opt.MinHosts)
	if minHosts <= 0 {
		minHosts = initialHosts
	}
	maxHosts := int(opt.MaxHosts)
	if maxHosts <= 0 {
		maxHosts = initialHosts
	}
	if maxHosts < minHosts {
		maxHosts = minHosts
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Snapshot metrics
			runMetrics := metrics.ConvertToRunMetrics(collector, serviceLabels)
			currentP95 := runMetrics.LatencyP95

			// Update best score and emit progress for SSE
			if currentP95 < bestScore {
				bestScore = currentP95
				iter++
				e.store.SetOptimizationProgress(runID, iter, bestScore)
			}

			// Host-level controller: when latency is above target and hosts are hot, scale
			// out hosts up to max_hosts; once that bound is reached, scale host capacity
			// vertically by increasing CPU cores per host.
			hostCount := rm.HostCount()
			maxHostCPU := rm.MaxHostCPUUtilization()

			if currentP95 > targetP95*1.05 && hostCount > 0 {
				if hostCount < maxHosts && maxHostCPU >= hostCPUHighThreshold {
					prevConfig, _ := e.GetRunConfiguration(runID)
					if err := rm.ScaleOutHosts(hostCount + 1); err != nil {
						logger.Error("online controller failed to scale out hosts",
							"run_id", runID,
							"current_hosts", hostCount,
							"target_hosts", hostCount+1,
							"error", err)
					} else {
						logger.Info("online controller scaled out hosts",
							"run_id", runID,
							"previous_hosts", hostCount,
							"new_hosts", rm.HostCount(),
							"max_hosts", maxHosts,
							"max_host_cpu_utilization", maxHostCPU)
						if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
							stepIndex++
							e.recordOptimizationStep(runID, stepIndex, targetP95, currentP95,
								"p95 above target, host CPU hot, scaled out hosts",
								prevConfig, currConfig)
						}
					}
				} else if hostCount >= maxHosts && maxHostCPU >= hostCPUHighThreshold {
					hostCPUStep := int(math.Ceil(opt.StepSize))
					if hostCPUStep < 1 {
						hostCPUStep = 1
					}
					prevConfig, _ := e.GetRunConfiguration(runID)
					rm.IncreaseHostCapacity(hostCPUStep, 0)
					logger.Info("online controller increased host capacity",
						"run_id", runID,
						"cpu_step", hostCPUStep,
						"host_count", rm.HostCount(),
						"max_hosts", maxHosts,
						"max_host_cpu_utilization", maxHostCPU)
					if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
						stepIndex++
						e.recordOptimizationStep(runID, stepIndex, targetP95, currentP95,
							"p95 above target, hosts at max, increased host CPU capacity",
							prevConfig, currConfig)
					}
				}
			}

			// Service-level controller: use p95 latency as the primary target, and CPU
			// utilization as a guardrail to choose between horizontal scaling (replicas)
			// and vertical scaling (CPU cores per instance). For now, we treat all
			// services symmetrically.
			step := int(opt.StepSize)
			if step < 1 {
				step = 1
			}
			cpuStep := opt.StepSize
			if cpuStep <= 0 {
				cpuStep = 1.0
			}

			for _, svc := range scenario.Services {
				// Current replicas from resource manager.
				currentReplicas := rm.ActiveReplicas(svc.ID)
				if currentReplicas < 1 {
					currentReplicas = 1
				}

				newReplicas := currentReplicas

				// Current per-instance CPU cores (assume homogeneous instances).
				instances := rm.GetInstancesForService(svc.ID)
				currentCores := resource.DefaultInstanceCPUCores
				if len(instances) > 0 {
					currentCores = instances[0].CPUCores()
				}
				newCPUCores := currentCores

				// Service-level CPU utilization (if available).
				var svcCPUUtil float64
				if runMetrics.ServiceMetrics != nil {
					if sm := runMetrics.ServiceMetrics[svc.ID]; sm != nil {
						svcCPUUtil = sm.CPUUtilization
					}
				}

				scaledVertically := false

				switch {
				case currentP95 > targetP95*1.05:
					// Above target: add capacity. If CPU is already hot, prefer vertical scaling
					// (more cores per instance); otherwise, scale replicas.
					if svcCPUUtil >= cpuHighThreshold {
						newCPUCores = currentCores + cpuStep
						scaledVertically = true
					} else {
						newReplicas = currentReplicas + step
					}
				case currentP95 < targetP95*0.7 && currentReplicas > 1:
					// Well below target: scale replicas down, but only if CPU is not already hot;
					// CPU acts as a guardrail to avoid over-consolidation under high load.
					if svcCPUUtil < cpuHighThreshold {
						newReplicas = currentReplicas - 1
					}
				default:
					// Within band; no change
				}

				// Apply vertical scaling first if requested.
				if scaledVertically && newCPUCores != currentCores {
					prevConfig, _ := e.GetRunConfiguration(runID)
					if err := e.UpdateServiceResources(runID, svc.ID, newCPUCores, 0); err != nil {
						logger.Error("online controller failed to update service resources",
							"run_id", runID,
							"service_id", svc.ID,
							"old_cpu_cores", currentCores,
							"new_cpu_cores", newCPUCores,
							"error", err)
						// Fallback: if we were trying to add capacity and vertical scaling
						// failed (e.g., host capacity), fall back to horizontal scale-up.
						if currentP95 > targetP95*1.05 {
							newReplicas = currentReplicas + step
						}
					} else {
						logger.Info("online controller updated service resources",
							"run_id", runID,
							"service_id", svc.ID,
							"old_cpu_cores", currentCores,
							"new_cpu_cores", newCPUCores,
							"p95_ms", currentP95,
							"target_p95_ms", targetP95,
							"cpu_utilization", svcCPUUtil)
						if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
							stepIndex++
							e.recordOptimizationStep(runID, stepIndex, targetP95, currentP95,
								"p95 above target, service CPU hot, scaled CPU cores",
								prevConfig, currConfig)
						}
						continue
					}
				}

				if newReplicas != currentReplicas {
					prevConfig, _ := e.GetRunConfiguration(runID)
					if err := e.UpdateServiceReplicas(runID, svc.ID, newReplicas); err != nil {
						logger.Error("online controller failed to update replicas",
							"run_id", runID,
							"service_id", svc.ID,
							"old", currentReplicas,
							"new", newReplicas,
							"error", err)
					} else {
						logger.Info("online controller updated replicas",
							"run_id", runID,
							"service_id", svc.ID,
							"old", currentReplicas,
							"new", newReplicas,
							"p95_ms", currentP95,
							"target_p95_ms", targetP95)
						if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
							reason := "p95 above target, scaled replicas up"
							if newReplicas < currentReplicas {
								reason = "p95 below target, scaled replicas down"
							}
							stepIndex++
							e.recordOptimizationStep(runID, stepIndex, targetP95, currentP95,
								reason, prevConfig, currConfig)
						}
					}
				}
			}
		}
	}
}

// convertMetricsToProto converts engine RunMetrics to protobuf RunMetrics
func convertMetricsToProto(engineMetrics *models.RunMetrics) *simulationv1.RunMetrics {
	pbMetrics := &simulationv1.RunMetrics{
		TotalRequests:      engineMetrics.TotalRequests,
		SuccessfulRequests: engineMetrics.SuccessfulRequests,
		FailedRequests:     engineMetrics.FailedRequests,
		LatencyP50Ms:       engineMetrics.LatencyP50,
		LatencyP95Ms:       engineMetrics.LatencyP95,
		LatencyP99Ms:       engineMetrics.LatencyP99,
		LatencyMeanMs:      engineMetrics.LatencyMean,
		ThroughputRps:      engineMetrics.ThroughputRPS,
	}

	// Convert service metrics
	if engineMetrics.ServiceMetrics != nil {
		for serviceName, svcMetrics := range engineMetrics.ServiceMetrics {
			// Safe conversion: ActiveReplicas is int, ensure it fits in int32
			var activeReplicas int32
			switch {
			case svcMetrics.ActiveReplicas < 0:
				activeReplicas = 0
			case svcMetrics.ActiveReplicas > math.MaxInt32:
				activeReplicas = math.MaxInt32
			default:
				activeReplicas = int32(svcMetrics.ActiveReplicas)
			}
			pbSvcMetrics := &simulationv1.ServiceMetrics{
				ServiceName:       serviceName,
				RequestCount:      svcMetrics.RequestCount,
				ErrorCount:        svcMetrics.ErrorCount,
				LatencyP50Ms:      svcMetrics.LatencyP50,
				LatencyP95Ms:      svcMetrics.LatencyP95,
				LatencyP99Ms:      svcMetrics.LatencyP99,
				LatencyMeanMs:     svcMetrics.LatencyMean,
				CpuUtilization:    svcMetrics.CPUUtilization,
				MemoryUtilization: svcMetrics.MemoryUtilization,
				ActiveReplicas:    activeReplicas,
			}
			pbMetrics.ServiceMetrics = append(pbMetrics.ServiceMetrics, pbSvcMetrics)
		}
	}

	return pbMetrics
}
