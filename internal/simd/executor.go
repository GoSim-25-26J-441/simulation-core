package simd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
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

func applyThroughputFromSimDuration(m *models.RunMetrics, simDuration time.Duration) {
	if m == nil || simDuration <= 0 {
		return
	}
	sec := simDuration.Seconds()
	if sec <= 0 {
		return
	}
	m.ThroughputRPS = float64(m.TotalRequests) / sec
	m.IngressThroughputRPS = float64(m.IngressRequests) / sec
}

// effectiveRunSeed returns RunInput.seed when non-zero; otherwise a single bootstrap value for the run
// (so scenario RNG, interaction manager, and workload generator share one base per execution).
func effectiveRunSeed(input *simulationv1.RunInput) int64 {
	if input == nil {
		return time.Now().UnixNano()
	}
	if s := input.GetSeed(); s != 0 {
		return s
	}
	return time.Now().UnixNano()
}

// RunExecutor manages asynchronous run execution and per-run cancellation.
type RunExecutor struct {
	store     *RunStore
	notifier  *Notifier
	limits    SimulationLimits
	limitsErr error

	optimizationRunner OptimizationRunner // optional; when set, optimization runs use it

	mu                     sync.Mutex
	cancels                map[string]context.CancelFunc
	workloadStates         map[string]*WorkloadState    // key: runID
	resourceManagers       map[string]*resource.Manager // key: runID; for dynamic replica updates
	policyManagers         map[string]*policy.Manager   // key: runID; for dynamic policy updates
	onlineCompletionReason map[string]string            // pending COMPLETED reason for online lease limits
	onlineLeaseDeadline    map[string]time.Time         // wall-clock heartbeat deadline per run
	// runScenarios holds the parsed scenario per active run for configuration/metadata export.
	runScenarios map[string]*config.Scenario
}

// SetOptimizationRunner sets the optimization runner for multi-run experiments.
// Must be called before starting optimization runs.
func (e *RunExecutor) SetOptimizationRunner(r OptimizationRunner) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.optimizationRunner = r
}

// defaultBatchMaxEvaluations is applied when optimization.batch is set and the client
// omits optimization.max_evaluations (0). Hill-climbing runs keep 0 = unlimited.
const defaultBatchMaxEvaluations int32 = 64

func applyMaxEvaluationsFromOpt(params *OptimizationParams, opt *simulationv1.OptimizationConfig) {
	if opt == nil {
		return
	}
	if opt.MaxEvaluations > 0 {
		params.MaxEvaluations = opt.MaxEvaluations
	} else if opt.GetBatch() != nil {
		params.MaxEvaluations = defaultBatchMaxEvaluations
	}
}

var (
	ErrRunNotFound               = errors.New("run not found")
	ErrRunTerminal               = errors.New("run is terminal")
	ErrRunIDMissing              = errors.New("run_id is required")
	ErrOnlineRunConcurrencyLimit = errors.New("online run concurrency limit")
)

func NewRunExecutor(store *RunStore, callbackWhitelist []string) *RunExecutor {
	limits, limitsErr := simulationLimitsFromEnv()
	return &RunExecutor{
		store:                  store,
		notifier:               NewNotifierWithWhitelist(callbackWhitelist),
		limits:                 limits,
		limitsErr:              limitsErr,
		cancels:                make(map[string]context.CancelFunc),
		workloadStates:         make(map[string]*WorkloadState),
		resourceManagers:       make(map[string]*resource.Manager),
		policyManagers:         make(map[string]*policy.Manager),
		onlineCompletionReason: make(map[string]string),
		onlineLeaseDeadline:    make(map[string]time.Time),
		runScenarios:           make(map[string]*config.Scenario),
	}
}

// Start begins executing a run asynchronously.
// Returns the updated run state (RUNNING) or an error.
func (e *RunExecutor) Start(runID string) (*RunRecord, error) {
	if runID == "" {
		return nil, ErrRunIDMissing
	}

	updated, err := e.store.SetStatusRunningWithOnlineConcurrencyGuard(runID)
	if err != nil {
		return nil, err
	}
	if e.limitsErr != nil {
		err := fmt.Errorf("invalid SIMD guardrail configuration: %w", e.limitsErr)
		if _, serr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, err.Error()); serr != nil {
			logger.Error("failed to set failed status after limits config error", "run_id", runID, "error", serr)
		}
		return nil, err
	}
	if err := e.limits.validatePreStart(updated.Input); err != nil {
		if _, serr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, err.Error()); serr != nil {
			logger.Error("failed to set failed status after prestart guardrail rejection", "run_id", runID, "error", serr)
		}
		return nil, err
	}
	if opt := updated.Input.GetOptimization(); opt != nil && opt.Online {
		if err := validateOnlineOptimizationConfig(opt); err != nil {
			if _, serr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, err.Error()); serr != nil {
				logger.Error("failed to set failed status after optimization validation", "run_id", runID, "error", serr)
			}
			return nil, err
		}
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
	if opt := updated.Input.Optimization; opt != nil {
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
	e.snapshotFinalConfiguration(runID)
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
	delete(e.runScenarios, runID)
	delete(e.onlineCompletionReason, runID)
	delete(e.onlineLeaseDeadline, runID)
	e.mu.Unlock()
}

// signalOnlineLeaseEnd requests a normal completion for an online run (COMPLETED + online_completion_reason).
// It stops the engine and cancels the run context; runOnlineOptimization finalizes metrics and status.
func (e *RunExecutor) signalOnlineLeaseEnd(runID, reason string) {
	e.mu.Lock()
	if e.onlineCompletionReason == nil {
		e.onlineCompletionReason = make(map[string]string)
	}
	e.onlineCompletionReason[runID] = reason
	cancel := e.cancels[runID]
	ws := e.workloadStates[runID]
	e.mu.Unlock()

	if ws != nil {
		if eng := ws.Engine(); eng != nil {
			eng.Stop()
		}
	}
	if cancel != nil {
		cancel()
	}
}

func (e *RunExecutor) takeOnlineCompletionReason(runID string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.onlineCompletionReason == nil {
		return ""
	}
	r := e.onlineCompletionReason[runID]
	delete(e.onlineCompletionReason, runID)
	return r
}

func (e *RunExecutor) setOnlineLeaseDeadline(runID string, deadline time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.onlineLeaseDeadline == nil {
		e.onlineLeaseDeadline = make(map[string]time.Time)
	}
	e.onlineLeaseDeadline[runID] = deadline
}

func (e *RunExecutor) onlineLeaseExpired(runID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.onlineLeaseDeadline == nil {
		return false
	}
	until, ok := e.onlineLeaseDeadline[runID]
	return ok && !until.IsZero() && time.Now().After(until)
}

// RenewOnlineLease extends the wall-clock lease for a running online run that uses lease_ttl_ms.
func (e *RunExecutor) RenewOnlineLease(runID string) (*RunRecord, error) {
	if runID == "" {
		return nil, ErrRunIDMissing
	}
	rec, ok := e.store.Get(runID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		return nil, fmt.Errorf("run is not running")
	}
	opt := rec.Input.GetOptimization()
	if opt == nil || !opt.Online || opt.GetLeaseTtlMs() <= 0 {
		return nil, fmt.Errorf("lease not configured for this run")
	}
	ttl := time.Duration(opt.GetLeaseTtlMs()) * time.Millisecond
	e.mu.Lock()
	if e.onlineLeaseDeadline == nil {
		e.onlineLeaseDeadline = make(map[string]time.Time)
	}
	e.onlineLeaseDeadline[runID] = time.Now().Add(ttl)
	e.mu.Unlock()

	rec2, ok := e.store.Get(runID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	return rec2, nil
}

// getCallbackSecret extracts the callback secret from a run record, returning empty string if not set
func getCallbackSecret(rec *RunRecord) string {
	if rec == nil || rec.Input == nil {
		return ""
	}
	return rec.Input.CallbackSecret
}

// snapshotFinalConfiguration persists the current effective configuration while the executor
// still holds workload/resource state (before cleanup). No-op if configuration is unavailable.
func (e *RunExecutor) snapshotFinalConfiguration(runID string) {
	if cfg, ok := e.GetRunConfiguration(runID); ok && cfg != nil {
		if err := e.store.SetFinalConfiguration(runID, cfg); err != nil {
			logger.Debug("set final configuration", "run_id", runID, "error", err)
		}
	}
}

// sendNotificationIfConfigured sends a notification to the callback URL if configured in the run record
func (e *RunExecutor) sendNotificationIfConfigured(rec *RunRecord) {
	if rec == nil || rec.Input == nil || rec.Input.CallbackUrl == "" {
		return
	}
	e.snapshotFinalConfiguration(rec.Run.Id)
	if refreshed, ok := e.store.Get(rec.Run.Id); ok {
		rec = refreshed
	}
	var resources map[string]any
	e.mu.Lock()
	rm := e.resourceManagers[rec.Run.Id]
	e.mu.Unlock()
	if rm != nil {
		snapshotAt := rm.LastSimTime()
		if snapshotAt.IsZero() {
			snapshotAt = time.Now()
		}
		queueSnaps := rm.QueueBrokerHealthSnapshots(snapshotAt)
		topicSnaps := rm.TopicBrokerHealthSnapshots(snapshotAt)
		queues := make([]map[string]any, 0, len(queueSnaps))
		for _, q := range queueSnaps {
			queues = append(queues, map[string]any{
				"broker_service":        q.BrokerID,
				"topic":                 q.Topic,
				"depth":                 q.Depth,
				"in_flight":             q.InFlight,
				"max_concurrency":       q.MaxConcurrency,
				"consumer_target":       q.ConsumerTarget,
				"oldest_message_age_ms": q.OldestMessageAgeMs,
				"drop_count":            q.DropCount,
				"redelivery_count":      q.RedeliveryCount,
				"dlq_count":             q.DlqCount,
			})
		}
		topics := make([]map[string]any, 0, len(topicSnaps))
		for _, t := range topicSnaps {
			topics = append(topics, map[string]any{
				"broker_service":        t.BrokerID,
				"topic":                 t.Topic,
				"partition":             t.Partition,
				"subscriber":            t.Subscriber,
				"consumer_group":        t.ConsumerGroup,
				"depth":                 t.Depth,
				"in_flight":             t.InFlight,
				"max_concurrency":       t.MaxConcurrency,
				"consumer_target":       t.ConsumerTarget,
				"oldest_message_age_ms": t.OldestMessageAgeMs,
				"drop_count":            t.DropCount,
				"redelivery_count":      t.RedeliveryCount,
				"dlq_count":             t.DlqCount,
			})
		}
		resources = map[string]any{
			"queues": queues,
			"topics": topics,
		}
	}
	e.notifier.Notify(rec.Input.CallbackUrl, getCallbackSecret(rec), rec, resources)
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
		Objective:      "p95_latency_ms",
		MaxIterations:  10,
		StepSize:       1.0,
		MaxEvaluations: 0,
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
		applyMaxEvaluationsFromOpt(params, opt)
		params.TargetUtilLow = opt.GetTargetUtilLow()
		params.TargetUtilHigh = opt.GetTargetUtilHigh()
		if opt.GetBatch() != nil {
			params.Batch = opt.GetBatch()
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

	logger.Info("starting optimization run", "run_id", runID, "objective", params.Objective,
		"max_iterations", params.MaxIterations, "max_evaluations", params.MaxEvaluations, "batch", params.Batch != nil)

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

// finalizeOnlineOptimizationRun aggregates metrics and marks the run COMPLETED with an optional online_completion_reason.
// simDuration, when positive, overrides aggregate and ingress throughput to use simulated time (not wall-clock collector duration).
func (e *RunExecutor) finalizeOnlineOptimizationRun(runID string, scenario *config.Scenario, rm *resource.Manager, metricsCollector *metrics.Collector, onlineReason string, simDuration time.Duration) {
	metricsCollector.Stop()
	serviceLabels := make([]map[string]string, 0, len(scenario.Services))
	for _, svc := range scenario.Services {
		serviceLabels = append(serviceLabels, metrics.CreateServiceLabels(svc.ID))
	}
	engineMetrics := metrics.ConvertToRunMetrics(metricsCollector, serviceLabels, e.runMetricsOptsForRun(runID))
	attachHostMetrics(scenario, rm, engineMetrics, metricsCollector)
	applyThroughputFromSimDuration(engineMetrics, simDuration)
	for _, svc := range scenario.Services {
		if sm := engineMetrics.ServiceMetrics[svc.ID]; sm != nil {
			sm.ActiveReplicas = rm.ActiveReplicas(svc.ID)
		}
	}
	pbMetrics := convertMetricsToProto(engineMetrics)
	if err := e.store.SetMetrics(runID, pbMetrics); err != nil {
		logger.Error("failed to set metrics for online run", "run_id", runID, "error", err)
	}
	if onlineReason != "" {
		if err := e.store.SetOnlineCompletionReason(runID, onlineReason); err != nil {
			logger.Error("failed to set online completion reason", "run_id", runID, "error", err)
		}
	}
	rec, ok := e.store.Get(runID)
	if !ok || rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		return
	}
	n := len(rec.OptimizationHistory)
	steps := int32(n)
	if n > math.MaxInt32 {
		steps = math.MaxInt32
	}
	if err := e.store.SetOptimizationResult(runID, runID, 0, steps, []string{runID}); err != nil {
		logger.Error("failed to set optimization result for online run", "run_id", runID, "error", err)
	}
	e.snapshotFinalConfiguration(runID)
	if updated, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_COMPLETED, ""); err != nil {
		logger.Error("failed to set completed status for online run", "run_id", runID, "error", err)
	} else {
		logger.Info("online optimization run completed", "run_id", runID, "online_completion_reason", onlineReason)
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
	eng.SetRuntimeLimits(e.limits.toEngineRuntimeLimits())
	eng.GetRunManager().SetMaxRequestsTracked(e.limits.MaxRequestsTracked, func(currentCount, max int) {
		eng.TriggerLimitExceeded(&engine.LimitExceededError{
			Limit: "max_requests_tracked",
			Value: int64(currentCount),
			Max:   int64(max),
		})
	})

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
	if opt.GetDrainTimeoutMs() > 0 {
		rm.SetScaleDownDrainTimeout(time.Duration(opt.GetDrainTimeoutMs()) * time.Millisecond)
	}

	// Initialize metrics collector
	metricsCollector := metrics.NewCollector()
	metricsCollector.SetMaxPoints(e.limits.MaxMetricPoints, func(currentCount, max int) {
		eng.TriggerLimitExceeded(&engine.LimitExceededError{
			Limit: "max_metric_points",
			Value: int64(currentCount),
			Max:   int64(max),
		})
	})
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

	runSeed := effectiveRunSeed(rec.Input)

	// Create scenario state and register handlers
	state, err := newScenarioState(scenario, rm, metricsCollector, policies, runSeed)
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
	state.SetSimEndTime(endTime)
	ScheduleDrainSweepKickoff(eng, startTime)
	workloadState := NewWorkloadState(runID, eng, endTime, runSeed)
	if err := workloadState.Start(scenario, startTime, true); err != nil {
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
	e.runScenarios[runID] = scenario
	e.mu.Unlock()

	if opt.GetLeaseTtlMs() > 0 {
		ttl := time.Duration(opt.GetLeaseTtlMs()) * time.Millisecond
		e.setOnlineLeaseDeadline(runID, time.Now().Add(ttl))
	}

	if opt.GetMaxOnlineDurationMs() > 0 {
		wallDur := time.Duration(opt.GetMaxOnlineDurationMs()) * time.Millisecond
		go func() {
			timer := time.NewTimer(wallDur)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				e.signalOnlineLeaseEnd(runID, OnlineCompletionDurationLimit)
			}
		}()
	}

	// Start the online controller loop
	go e.runOnlineController(ctx, runID, scenario, metricsCollector, opt, rm, state)

	// Run simulation; wall-clock limits use signalOnlineLeaseEnd; explicit stop uses StopRun.
	logger.Info("starting online optimization run", "run_id", runID, "duration", onlineRunDuration)
	if err := eng.Run(onlineRunDuration); err != nil {
		// If cancelled, handle based on current run status.
		if ctx.Err() != nil {
			reason := e.takeOnlineCompletionReason(runID)
			if reason != "" {
				e.finalizeOnlineOptimizationRun(runID, scenario, rm, metricsCollector, reason, eng.GetSimTime().Sub(startTime))
				return
			}

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
				engineMetrics := metrics.ConvertToRunMetrics(metricsCollector, serviceLabels, e.runMetricsOptsForRun(runID))
				attachHostMetrics(scenario, rm, engineMetrics, metricsCollector)
				applyThroughputFromSimDuration(engineMetrics, eng.GetSimTime().Sub(startTime))
				for _, svc := range scenario.Services {
					if sm := engineMetrics.ServiceMetrics[svc.ID]; sm != nil {
						sm.ActiveReplicas = rm.ActiveReplicas(svc.ID)
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
	e.finalizeOnlineOptimizationRun(runID, scenario, rm, metricsCollector, "", simDuration)
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
	eng.SetRuntimeLimits(e.limits.toEngineRuntimeLimits())
	eng.GetRunManager().SetMaxRequestsTracked(e.limits.MaxRequestsTracked, func(currentCount, max int) {
		eng.TriggerLimitExceeded(&engine.LimitExceededError{
			Limit: "max_requests_tracked",
			Value: int64(currentCount),
			Max:   int64(max),
		})
	})

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
	metricsCollector.SetMaxPoints(e.limits.MaxMetricPoints, func(currentCount, max int) {
		eng.TriggerLimitExceeded(&engine.LimitExceededError{
			Limit: "max_metric_points",
			Value: int64(currentCount),
			Max:   int64(max),
		})
	})
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

	runSeed := effectiveRunSeed(rec.Input)

	// Create scenario state and register handlers
	state, err := newScenarioState(scenario, rm, metricsCollector, policies, runSeed)
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
	state.SetSimEndTime(endTime)
	ScheduleDrainSweepKickoff(eng, startTime)
	workloadState := NewWorkloadState(runID, eng, endTime, runSeed)
	if err := workloadState.Start(scenario, startTime, rec.Input.RealTimeMode); err != nil {
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
	e.runScenarios[runID] = scenario
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
	engineMetrics := metrics.ConvertToRunMetrics(metricsCollector, serviceLabels, e.runMetricsOptsForRun(runID))
	attachHostMetrics(scenario, rm, engineMetrics, metricsCollector)
	// For completed runs, use simulation duration for throughput so non-real-time
	// mode reports requests over simulated time instead of wall-clock execution time.
	applyThroughputFromSimDuration(engineMetrics, simDuration)

	// Populate ActiveReplicas from the resource manager (live routable count)
	for _, svc := range scenario.Services {
		if sm := engineMetrics.ServiceMetrics[svc.ID]; sm != nil {
			sm.ActiveReplicas = rm.ActiveReplicas(svc.ID)
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
		e.snapshotFinalConfiguration(runID)
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

// allowScaleDownReplicas returns true if the controller may scale down replicas given
// current CPU/memory utilization and optional scale-down thresholds. When both
// scaleDownCPUMax and scaleDownMemMax are 0, only the hot-CPU guard (0.8) applies.
func allowScaleDownReplicas(svcCPUUtil, svcMemUtil, scaleDownCPUMax, scaleDownMemMax float64) bool {
	const cpuHotThreshold = 0.8
	if svcCPUUtil >= cpuHotThreshold {
		return false
	}
	if scaleDownCPUMax <= 0 && scaleDownMemMax <= 0 {
		return true
	}
	if scaleDownCPUMax > 0 && svcCPUUtil >= scaleDownCPUMax {
		return false
	}
	if scaleDownMemMax > 0 && svcMemUtil >= scaleDownMemMax {
		return false
	}
	return true
}

func serviceQueueDepthTotal(rm *resource.Manager, serviceID string) int {
	total := 0
	for _, inst := range rm.GetInstancesForService(serviceID) {
		total += inst.QueueLength()
	}
	return total
}

type brokerPressureSignal struct {
	HasBacklog     bool
	HasInFlight    bool
	HasDrops       bool
	HasDLQ         bool
	MaxDepth       int
	MaxOldestAgeMs float64
	Reason         string
}

func targetServiceIDFromConsumerTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	parts := strings.SplitN(target, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func brokerPressureByConsumerService(rm *resource.Manager, now time.Time) map[string]brokerPressureSignal {
	out := make(map[string]brokerPressureSignal)
	if rm == nil {
		return out
	}
	acc := func(svc, reason string, depth int, inFlight bool, oldestAge float64, hasDrops, hasDLQ bool) {
		if svc == "" {
			return
		}
		cur := out[svc]
		if depth > cur.MaxDepth {
			cur.MaxDepth = depth
		}
		if oldestAge > cur.MaxOldestAgeMs {
			cur.MaxOldestAgeMs = oldestAge
		}
		cur.HasBacklog = cur.HasBacklog || depth > 0
		cur.HasInFlight = cur.HasInFlight || inFlight
		cur.HasDrops = cur.HasDrops || hasDrops
		cur.HasDLQ = cur.HasDLQ || hasDLQ
		if cur.Reason == "" {
			cur.Reason = reason
		}
		out[svc] = cur
	}

	for _, q := range rm.QueueBrokerHealthSnapshots(now) {
		svc := targetServiceIDFromConsumerTarget(q.ConsumerTarget)
		acc(svc, "queue_pressure", q.Depth, q.InFlight > 0, q.OldestMessageAgeMs, q.DropCount > 0, q.DlqCount > 0)
	}
	for _, t := range rm.TopicBrokerHealthSnapshots(now) {
		svc := targetServiceIDFromConsumerTarget(t.ConsumerTarget)
		acc(svc, "topic_pressure", t.Depth, t.InFlight > 0, t.OldestMessageAgeMs, t.DropCount > 0, t.DlqCount > 0)
	}
	return out
}

// onlineScaleDownGuard reports whether replica or vertical scale-down should be
// skipped due to latency near target, load on queues, concurrency, or rising errors.
func onlineScaleDownGuard(rm *resource.Manager, runMetrics *models.RunMetrics, svcID string, targetP95 float64, prevErrFrac float64, pressure map[string]brokerPressureSignal) bool {
	if runMetrics == nil {
		return true
	}
	if targetP95 > 0 && runMetrics.LatencyP95 >= targetP95*0.95 {
		return true
	}
	if sm := runMetrics.ServiceMetrics[svcID]; sm != nil && sm.ConcurrentRequests > 10 {
		return true
	}
	if serviceQueueDepthTotal(rm, svcID) > 0 {
		return true
	}
	if p, ok := pressure[svcID]; ok && (p.HasBacklog || p.HasInFlight || p.HasDrops || p.HasDLQ || p.MaxOldestAgeMs > 0) {
		return true
	}
	tot := float64(runMetrics.TotalRequests)
	if tot > 0 && prevErrFrac >= 0 {
		curr := float64(runMetrics.FailedRequests) / tot
		if curr > prevErrFrac+0.005 {
			return true
		}
	}
	return false
}

// onlineTopologyGuard is a compatibility helper for topology-aware scale-down checks.
// It returns a stable reason string so tests and audit messages can assert decisions.
func onlineTopologyGuard(runMetrics *models.RunMetrics, _ *config.Scenario, _ string, _ *resource.Manager, opt *simulationv1.OptimizationConfig, _ int) (bool, string) {
	if runMetrics == nil || opt == nil {
		return false, ""
	}
	if opt.MinLocalityHitRate > 0 && runMetrics.LocalityHitRate < opt.MinLocalityHitRate {
		return true, "locality_hit_rate_below_min"
	}
	if opt.MaxCrossZoneRequestFraction > 0 && runMetrics.CrossZoneRequestFraction > opt.MaxCrossZoneRequestFraction {
		return true, "cross_zone_fraction_above_max"
	}
	if opt.MaxTopologyLatencyPenaltyMeanMs > 0 && runMetrics.TopologyLatencyPenaltyMsMean > opt.MaxTopologyLatencyPenaltyMeanMs {
		return true, "topology_latency_penalty_above_max"
	}
	return false, ""
}

// maxServiceUtilization returns the max CPU or memory utilization across non-client services.
// Client services (id starting with "client") are skipped. Returns 0 if no services.
func maxServiceUtilization(runMetrics *models.RunMetrics, kind string) float64 {
	if runMetrics == nil || runMetrics.ServiceMetrics == nil {
		return 0
	}
	var maxUtil float64
	for svcID, sm := range runMetrics.ServiceMetrics {
		if sm == nil || strings.HasPrefix(strings.ToLower(svcID), "client") {
			continue
		}
		var u float64
		if kind == "memory" {
			u = sm.MemoryUtilization
		} else {
			u = sm.CPUUtilization
		}
		if u > maxUtil {
			maxUtil = u
		}
	}
	return maxUtil
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

// validateOnlineOptimizationConfig rejects invalid online optimization inputs before a
// run transitions to RUNNING (e.g. p95-primary requires a positive latency target).
func validateOnlineOptimizationConfig(opt *simulationv1.OptimizationConfig) error {
	if opt == nil || !opt.Online {
		return nil
	}
	primary := strings.ToLower(strings.TrimSpace(opt.GetOptimizationTargetPrimary()))
	if primary == "" {
		primary = "p95_latency"
	}
	if primary == "p95_latency" && opt.GetTargetP95LatencyMs() <= 0 {
		return fmt.Errorf("online optimization with primary target p95_latency requires target_p95_latency_ms > 0")
	}
	return nil
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
	state *scenarioState,
) {
	if opt == nil {
		return
	}

	targetP95 := opt.TargetP95LatencyMs
	primaryTargetCtl := strings.ToLower(strings.TrimSpace(opt.GetOptimizationTargetPrimary()))
	if primaryTargetCtl == "" {
		primaryTargetCtl = "p95_latency"
	}
	p95Guard := targetP95 > 0
	if primaryTargetCtl == "p95_latency" && targetP95 <= 0 {
		// p95-primary mode requires an explicit latency target.
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
	scaleDownCPUMax := opt.GetScaleDownCpuUtilMax()
	scaleDownMemMax := opt.GetScaleDownMemUtilMax()

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
	scaleDownHostCPUMax := opt.GetScaleDownHostCpuUtilMax()
	initialHostCores := 0
	if len(scenario.Hosts) > 0 {
		initialHostCores = scenario.Hosts[0].Cores
	}
	if initialHostCores < 1 {
		initialHostCores = 1
	}
	initialHostMemGB := 0
	if len(scenario.Hosts) > 0 {
		initialHostMemGB = scenario.Hosts[0].MemoryGB
	}
	if initialHostMemGB < 1 {
		initialHostMemGB = 1
	}

	lastScaleWall := time.Time{}
	stableRepDown := make(map[string]int)
	stableHostScaleIn := 0
	stableHostCPUDown := 0
	stableHostMemDown := 0
	stableVertCPUDown := make(map[string]int)
	stableVertMemDown := make(map[string]int)
	prevErrFrac := -1.0
	intervalMs := int64(interval / time.Millisecond)
	if intervalMs < 1 {
		intervalMs = 1
	}
	var noopStreak int32
	maxStepsNoticeLogged := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stepIndexBefore := stepIndex
			if lt := rm.LastSimTime(); !lt.IsZero() {
				dropped := rm.ProcessDrainingInstances(lt)
				e.mu.Lock()
				ws := e.workloadStates[runID]
				e.mu.Unlock()
				if ws != nil {
					if eng := ws.Engine(); eng != nil {
						failDroppedQueueRequests(eng, state, lt, dropped)
					}
				}
			}
			if opt.GetLeaseTtlMs() > 0 && e.onlineLeaseExpired(runID) {
				e.signalOnlineLeaseEnd(runID, OnlineCompletionHeartbeatExpired)
				continue
			}
			if opt.GetMaxControllerSteps() > 0 && stepIndex >= opt.GetMaxControllerSteps() {
				if !maxStepsNoticeLogged {
					logger.Warn("online controller reached max_controller_steps; stopping simulation",
						"run_id", runID,
						"max_controller_steps", opt.GetMaxControllerSteps())
					maxStepsNoticeLogged = true
				}
				e.signalOnlineLeaseEnd(runID, OnlineCompletionControllerSteps)
				continue
			}
			cooldown := time.Duration(opt.GetScaleDownCooldownMs()) * time.Millisecond
			if cooldown > 0 && !lastScaleWall.IsZero() && time.Since(lastScaleWall) < cooldown {
				continue
			}
			stabWindowMs := opt.GetScaleDownStabilizationWindowMs()
			stabTicks := 1
			if stabWindowMs > 0 {
				stabTicks = int((stabWindowMs + intervalMs - 1) / intervalMs)
				if stabTicks < 1 {
					stabTicks = 1
				}
			}
			minReplicasCtl := int(opt.GetMinReplicasPerService())
			if minReplicasCtl < 1 {
				minReplicasCtl = 1
			}
			minCPUCtl := opt.GetMinCpuCoresPerInstance()
			minMemCtl := opt.GetMinMemoryMbPerInstance()
			memHeadroomCtl := opt.GetMemoryDownsizeHeadroomMb()
			scaleDownHostMemMax := opt.GetScaleDownHostMemUtilMax()

			// Snapshot metrics
			runMetrics := metrics.ConvertToRunMetrics(collector, serviceLabels, e.runMetricsOptsForRun(runID))
			attachHostMetrics(scenario, rm, runMetrics, collector)
			currentP95 := runMetrics.LatencyP95
			snapshotAt := rm.LastSimTime()
			if snapshotAt.IsZero() {
				snapshotAt = time.Now()
			}
			brokerPressure := brokerPressureByConsumerService(rm, snapshotAt)

			// Compute current score from primary target (same metric used for scaling decisions)
			var currentScore float64
			lowerIsBetter := true
			switch primaryTargetCtl {
			case "cpu_utilization":
				currentScore = maxServiceUtilization(runMetrics, "cpu")
			case "memory_utilization":
				currentScore = maxServiceUtilization(runMetrics, "memory")
			default:
				currentScore = currentP95
			}
			if lowerIsBetter && currentScore < bestScore {
				bestScore = currentScore
				iter++
				e.store.SetOptimizationProgress(runID, iter, bestScore)
			}

			// Host-level controller: when latency is above target and hosts are hot, scale
			// out hosts up to max_hosts; once that bound is reached, scale host capacity
			// vertically by increasing CPU cores per host.
			hostCount := rm.HostCount()
			maxHostCPU := rm.MaxHostCPUUtilization()
			maxHostMem := rm.MaxHostMemoryUtilization()

			if p95Guard && currentP95 > targetP95*1.05 && hostCount > 0 {
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

			// Host-level scale-in (stabilized): when P95 and host CPU are low, remove empty hosts.
			hostScaleInCond := p95Guard && scaleDownHostCPUMax > 0 && currentP95 < targetP95*0.7 && hostCount > minHosts && maxHostCPU < scaleDownHostCPUMax
			if hostScaleInCond {
				stableHostScaleIn++
			} else {
				stableHostScaleIn = 0
			}
			if hostScaleInCond && stableHostScaleIn >= stabTicks {
				prevConfig, _ := e.GetRunConfiguration(runID)
				if err := rm.ScaleInHosts(hostCount - 1); err != nil {
					logger.Debug("online controller scale-in hosts skipped",
						"run_id", runID,
						"host_count", hostCount,
						"error", err)
				} else if rm.HostCount() < hostCount {
					stableHostScaleIn = 0
					logger.Info("online controller scaled in hosts",
						"run_id", runID,
						"previous_hosts", hostCount,
						"new_hosts", rm.HostCount(),
						"min_hosts", minHosts)
					if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
						stepIndex++
						e.recordOptimizationStep(runID, stepIndex, targetP95, currentP95,
							"p95 and host utilization low, scaled in hosts",
							prevConfig, currConfig)
					}
				}
			}

			var currentHostCores int
			for _, hid := range rm.HostIDs() {
				if h, ok := rm.GetHost(hid); ok {
					currentHostCores = h.CPUCores()
					break
				}
			}
			hostCPUDownCond := scaleDownHostCPUMax > 0 && hostCount >= minHosts && maxHostCPU < scaleDownHostCPUMax && currentHostCores > initialHostCores
			if hostCPUDownCond {
				stableHostCPUDown++
			} else {
				stableHostCPUDown = 0
			}
			if hostCPUDownCond && stableHostCPUDown >= stabTicks {
				hostCPUStep := int(math.Ceil(opt.StepSize))
				if hostCPUStep < 1 {
					hostCPUStep = 1
				}
				prevConfig, _ := e.GetRunConfiguration(runID)
				if err := rm.DecreaseHostCapacity(-hostCPUStep, 0); err != nil {
					logger.Debug("online controller decrease host capacity skipped",
						"run_id", runID,
						"error", err)
				} else {
					stableHostCPUDown = 0
					logger.Info("online controller decreased host capacity",
						"run_id", runID,
						"cpu_step", hostCPUStep,
						"host_count", rm.HostCount())
					if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
						stepIndex++
						e.recordOptimizationStep(runID, stepIndex, targetP95, currentP95,
							"host utilization low, decreased host CPU capacity",
							prevConfig, currConfig)
					}
				}
			}

			var currentHostMemGB int
			for _, hid := range rm.HostIDs() {
				if h, ok := rm.GetHost(hid); ok {
					currentHostMemGB = h.MemoryGB()
					break
				}
			}
			hostMemDownCond := scaleDownHostMemMax > 0 && hostCount >= minHosts && maxHostMem < scaleDownHostMemMax && currentHostMemGB > initialHostMemGB
			if hostMemDownCond {
				stableHostMemDown++
			} else {
				stableHostMemDown = 0
			}
			if hostMemDownCond && stableHostMemDown >= stabTicks {
				hostMemStep := int(math.Ceil(opt.StepSize))
				if hostMemStep < 1 {
					hostMemStep = 1
				}
				prevConfig, _ := e.GetRunConfiguration(runID)
				if err := rm.DecreaseHostCapacity(0, -hostMemStep); err != nil {
					logger.Debug("online controller decrease host memory skipped",
						"run_id", runID,
						"error", err)
				} else {
					stableHostMemDown = 0
					logger.Info("online controller decreased host memory capacity",
						"run_id", runID,
						"memory_gb_step", hostMemStep,
						"host_count", rm.HostCount())
					if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
						stepIndex++
						e.recordOptimizationStep(runID, stepIndex, targetP95, currentP95,
							"host memory utilization low, decreased host memory capacity",
							prevConfig, currConfig)
					}
				}
			}

			// Service-level controller: primary target + utilization guardrails; scaling
			// dimensions are gated by service kind and scaling policy.
			p95OkForDown := !p95Guard || currentP95 <= targetP95*1.05
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

				// Current per-instance CPU/memory (prefer a routable instance).
				instances := rm.GetInstancesForService(svc.ID)
				currentCores := resource.DefaultInstanceCPUCores
				currentMemMB := resource.DefaultInstanceMemoryMB
				var routable *resource.ServiceInstance
				for _, inst := range instances {
					if inst.IsRoutable() {
						routable = inst
						break
					}
				}
				if routable != nil {
					currentCores = routable.CPUCores()
					currentMemMB = routable.MemoryMB()
				} else if len(instances) > 0 {
					currentCores = instances[0].CPUCores()
					currentMemMB = instances[0].MemoryMB()
				}
				newCPUCores := currentCores
				newMemMBVert := currentMemMB

				// Service-level CPU and memory utilization (if available).
				var svcCPUUtil, svcMemUtil float64
				if runMetrics.ServiceMetrics != nil {
					if sm := runMetrics.ServiceMetrics[svc.ID]; sm != nil {
						svcCPUUtil = sm.CPUUtilization
						svcMemUtil = sm.MemoryUtilization
					}
				}

				primaryTarget := primaryTargetCtl
				targetUtilHigh := opt.GetTargetUtilHigh()
				if targetUtilHigh <= 0 {
					targetUtilHigh = 0.7
				}
				targetUtilLow := opt.GetTargetUtilLow()
				if targetUtilLow <= 0 {
					targetUtilLow = 0.4
				}

				scaledVertically := false

				if primaryTarget == "cpu_utilization" || primaryTarget == "memory_utilization" {
					util := svcCPUUtil
					if primaryTarget == "memory_utilization" {
						util = svcMemUtil
					}
					// Utilization-driven: scale up when util > targetHigh, scale down when
					// util < targetLow and P95 guardrail allows (do not scale down if P95 would exceed target).
					switch {
					case brokerPressure[svc.ID].HasBacklog || brokerPressure[svc.ID].HasInFlight || brokerPressure[svc.ID].MaxOldestAgeMs > 0:
						if config.ServiceAllowsVerticalCPU(&svc) && svcCPUUtil >= cpuHighThreshold {
							newCPUCores = currentCores + cpuStep
							scaledVertically = true
						} else if config.ServiceAllowsHorizontalScaling(&svc) {
							newReplicas = currentReplicas + step
						}
					case util > targetUtilHigh:
						if config.ServiceAllowsVerticalCPU(&svc) && svcCPUUtil >= cpuHighThreshold {
							newCPUCores = currentCores + cpuStep
							scaledVertically = true
						} else if config.ServiceAllowsHorizontalScaling(&svc) {
							newReplicas = currentReplicas + step
						}
					case util < targetUtilLow && currentReplicas > 1 && p95OkForDown:
						if config.ServiceAllowsHorizontalScaling(&svc) && allowScaleDownReplicas(svcCPUUtil, svcMemUtil, scaleDownCPUMax, scaleDownMemMax) {
							newReplicas = currentReplicas - 1
						}
					}
				} else {
					// P95-primary (default): scale up on P95 above target, scale down on P95 below target with utilization gates.
					switch {
					case brokerPressure[svc.ID].HasBacklog || brokerPressure[svc.ID].HasInFlight || brokerPressure[svc.ID].MaxOldestAgeMs > 0:
						if config.ServiceAllowsVerticalCPU(&svc) && svcCPUUtil >= cpuHighThreshold {
							newCPUCores = currentCores + cpuStep
							scaledVertically = true
						} else if config.ServiceAllowsHorizontalScaling(&svc) {
							newReplicas = currentReplicas + step
						}
					case p95Guard && currentP95 > targetP95*1.05:
						if config.ServiceAllowsVerticalCPU(&svc) && svcCPUUtil >= cpuHighThreshold {
							newCPUCores = currentCores + cpuStep
							scaledVertically = true
						} else if config.ServiceAllowsHorizontalScaling(&svc) {
							newReplicas = currentReplicas + step
						}
					case p95Guard && currentP95 < targetP95*0.7 && currentReplicas > 1:
						if config.ServiceAllowsHorizontalScaling(&svc) && allowScaleDownReplicas(svcCPUUtil, svcMemUtil, scaleDownCPUMax, scaleDownMemMax) {
							newReplicas = currentReplicas - 1
						}
					}
				}

				// Utilization-primary: vertical CPU/memory downscale (stabilized like replica drain).
				wantVertCPU := primaryTarget == "cpu_utilization" && config.ServiceAllowsVerticalCPU(&svc) && svcCPUUtil < targetUtilLow && p95OkForDown && newReplicas >= currentReplicas &&
					allowScaleDownReplicas(svcCPUUtil, svcMemUtil, scaleDownCPUMax, scaleDownMemMax) &&
					!onlineScaleDownGuard(rm, runMetrics, svc.ID, targetP95, prevErrFrac, brokerPressure)
				var targetVertCPU float64
				if wantVertCPU {
					nc := currentCores - cpuStep
					if minCPUCtl > 0 && nc < minCPUCtl {
						nc = minCPUCtl
					}
					if nc+1e-9 < currentCores {
						targetVertCPU = nc
					} else {
						wantVertCPU = false
					}
				}
				if wantVertCPU {
					stableVertCPUDown[svc.ID]++
				} else {
					stableVertCPUDown[svc.ID] = 0
				}
				if wantVertCPU && stableVertCPUDown[svc.ID] >= stabTicks && targetVertCPU > 0 {
					newCPUCores = targetVertCPU
					scaledVertically = true
					stableVertCPUDown[svc.ID] = 0
				}

				wantVertMem := primaryTarget == "memory_utilization" && config.ServiceAllowsVerticalMemory(&svc) && svcMemUtil < targetUtilLow && p95OkForDown && newReplicas >= currentReplicas &&
					allowScaleDownReplicas(svcCPUUtil, svcMemUtil, scaleDownCPUMax, scaleDownMemMax) &&
					!onlineScaleDownGuard(rm, runMetrics, svc.ID, targetP95, prevErrFrac, brokerPressure)
				var targetVertMem float64
				if wantVertMem {
					nm := currentMemMB - float64(step)*128
					if minMemCtl > 0 && nm < minMemCtl {
						nm = minMemCtl
					}
					if nm+1e-9 < currentMemMB {
						targetVertMem = nm
					} else {
						wantVertMem = false
					}
				}
				if wantVertMem {
					stableVertMemDown[svc.ID]++
				} else {
					stableVertMemDown[svc.ID] = 0
				}
				if wantVertMem && stableVertMemDown[svc.ID] >= stabTicks && targetVertMem > 0 {
					newMemMBVert = targetVertMem
					stableVertMemDown[svc.ID] = 0
				}

				// Stabilization and guardrails for replica scale-down.
				if newReplicas < currentReplicas {
					stableRepDown[svc.ID]++
					if stableRepDown[svc.ID] < stabTicks {
						newReplicas = currentReplicas
					}
				} else {
					stableRepDown[svc.ID] = 0
				}
				if newReplicas < currentReplicas && onlineScaleDownGuard(rm, runMetrics, svc.ID, targetP95, prevErrFrac, brokerPressure) {
					newReplicas = currentReplicas
					stableRepDown[svc.ID] = 0
				}
				if newReplicas < minReplicasCtl {
					newReplicas = minReplicasCtl
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
						if config.ServiceAllowsHorizontalScaling(&svc) {
							if primaryTargetCtl == "cpu_utilization" || primaryTargetCtl == "memory_utilization" {
								newReplicas = currentReplicas + step
							} else if p95Guard && currentP95 > targetP95*1.05 {
								newReplicas = currentReplicas + step
							}
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

				if newMemMBVert+1e-9 < currentMemMB {
					prevConfig, _ := e.GetRunConfiguration(runID)
					if err := e.UpdateServiceResourcesWithHeadroom(runID, svc.ID, 0, newMemMBVert, memHeadroomCtl); err != nil {
						logger.Debug("online controller memory downscale skipped",
							"run_id", runID,
							"service_id", svc.ID,
							"error", err)
					} else {
						logger.Info("online controller decreased service memory",
							"run_id", runID,
							"service_id", svc.ID,
							"old_memory_mb", currentMemMB,
							"new_memory_mb", newMemMBVert)
						if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
							stepIndex++
							e.recordOptimizationStep(runID, stepIndex, targetP95, currentP95,
								"memory utilization low, decreased per-instance memory",
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
							"target_p95_ms", targetP95,
							"cpu_utilization", svcCPUUtil,
							"memory_utilization", svcMemUtil)
						if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
							reason := "p95 above target, scaled replicas up"
							if newReplicas < currentReplicas {
								reason = "p95 below target and utilization low, scaled replicas down"
								if primaryTarget == "cpu_utilization" || primaryTarget == "memory_utilization" {
									reason = "utilization below target and P95 ok, scaled replicas down"
								}
							} else if primaryTarget == "cpu_utilization" || primaryTarget == "memory_utilization" {
								reason = "utilization above target, scaled replicas up"
							}
							stepIndex++
							e.recordOptimizationStep(runID, stepIndex, targetP95, currentP95,
								reason, prevConfig, currConfig)
						}
					}
				}
			}

			if stepIndex > stepIndexBefore {
				lastScaleWall = time.Now()
			}
			if runMetrics.TotalRequests > 0 {
				prevErrFrac = float64(runMetrics.FailedRequests) / float64(runMetrics.TotalRequests)
			}

			if opt.GetMaxNoopIntervals() > 0 {
				if stepIndex == stepIndexBefore {
					noopStreak++
					if noopStreak >= opt.GetMaxNoopIntervals() {
						logger.Info("online controller converged (no configuration changes)",
							"run_id", runID, "noop_intervals", noopStreak)
						e.signalOnlineLeaseEnd(runID, OnlineCompletionConverged)
					}
				} else {
					noopStreak = 0
				}
			}
		}
	}
}

func attachHostMetrics(scenario *config.Scenario, rm *resource.Manager, engineMetrics *models.RunMetrics, collector *metrics.Collector) {
	if engineMetrics == nil || collector == nil {
		return
	}
	var ids []string
	if rm != nil {
		ids = rm.HostIDs()
	} else if scenario != nil {
		ids = make([]string, 0, len(scenario.Hosts))
		for _, h := range scenario.Hosts {
			ids = append(ids, h.ID)
		}
	}
	if len(ids) == 0 {
		return
	}
	metrics.AttachHostUtilization(engineMetrics, collector, ids)
}

// convertMetricsToProto converts engine RunMetrics to protobuf RunMetrics
func convertMetricsToProto(engineMetrics *models.RunMetrics) *simulationv1.RunMetrics {
	pbMetrics := &simulationv1.RunMetrics{
		TotalRequests:                  engineMetrics.TotalRequests,
		SuccessfulRequests:             engineMetrics.SuccessfulRequests,
		FailedRequests:                 engineMetrics.FailedRequests,
		LatencyP50Ms:                   engineMetrics.LatencyP50,
		LatencyP95Ms:                   engineMetrics.LatencyP95,
		LatencyP99Ms:                   engineMetrics.LatencyP99,
		LatencyMeanMs:                  engineMetrics.LatencyMean,
		ThroughputRps:                  engineMetrics.ThroughputRPS,
		IngressRequests:                engineMetrics.IngressRequests,
		InternalRequests:               engineMetrics.InternalRequests,
		IngressThroughputRps:           engineMetrics.IngressThroughputRPS,
		IngressFailedRequests:          engineMetrics.IngressFailedRequests,
		IngressErrorRate:               engineMetrics.IngressErrorRate,
		AttemptFailedRequests:          engineMetrics.AttemptFailedRequests,
		AttemptErrorRate:               engineMetrics.AttemptErrorRate,
		RetryAttempts:                  engineMetrics.RetryAttempts,
		TimeoutErrors:                  engineMetrics.TimeoutErrors,
		QueueEnqueueCountTotal:         engineMetrics.QueueEnqueueCountTotal,
		QueueDequeueCountTotal:         engineMetrics.QueueDequeueCountTotal,
		QueueDropCountTotal:            engineMetrics.QueueDropCountTotal,
		QueueRedeliveryCountTotal:      engineMetrics.QueueRedeliveryCountTotal,
		QueueDlqCountTotal:             engineMetrics.QueueDlqCountTotal,
		QueueDepthSum:                  engineMetrics.QueueDepthSum,
		TopicPublishCountTotal:         engineMetrics.TopicPublishCountTotal,
		TopicDeliverCountTotal:         engineMetrics.TopicDeliverCountTotal,
		TopicDropCountTotal:            engineMetrics.TopicDropCountTotal,
		TopicRedeliveryCountTotal:      engineMetrics.TopicRedeliveryCountTotal,
		TopicDlqCountTotal:             engineMetrics.TopicDlqCountTotal,
		TopicBacklogDepthSum:           engineMetrics.TopicBacklogDepthSum,
		TopicConsumerLagSum:            engineMetrics.TopicConsumerLagSum,
		QueueOldestMessageAgeMs:        engineMetrics.QueueOldestMessageAgeMs,
		TopicOldestMessageAgeMs:        engineMetrics.TopicOldestMessageAgeMs,
		MaxQueueDepth:                  engineMetrics.MaxQueueDepth,
		MaxTopicBacklogDepth:           engineMetrics.MaxTopicBacklogDepth,
		MaxTopicConsumerLag:            engineMetrics.MaxTopicConsumerLag,
		QueueDropRate:                  engineMetrics.QueueDropRate,
		TopicDropRate:                  engineMetrics.TopicDropRate,
		LocalityHitRate:                engineMetrics.LocalityHitRate,
		CrossZoneRequestCountTotal:     engineMetrics.CrossZoneRequestCountTotal,
		SameZoneRequestCountTotal:      engineMetrics.SameZoneRequestCountTotal,
		CrossZoneRequestFraction:       engineMetrics.CrossZoneRequestFraction,
		CrossZoneLatencyPenaltyMsTotal: engineMetrics.CrossZoneLatencyPenaltyMsTotal,
		CrossZoneLatencyPenaltyMsMean:  engineMetrics.CrossZoneLatencyPenaltyMsMean,
		SameZoneLatencyPenaltyMsTotal:  engineMetrics.SameZoneLatencyPenaltyMsTotal,
		SameZoneLatencyPenaltyMsMean:   engineMetrics.SameZoneLatencyPenaltyMsMean,
		ExternalLatencyMsTotal:         engineMetrics.ExternalLatencyMsTotal,
		ExternalLatencyMsMean:          engineMetrics.ExternalLatencyMsMean,
		TopologyLatencyPenaltyMsTotal:  engineMetrics.TopologyLatencyPenaltyMsTotal,
		TopologyLatencyPenaltyMsMean:   engineMetrics.TopologyLatencyPenaltyMsMean,
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
			var concurrentReqs int32
			switch {
			case svcMetrics.ConcurrentRequests < 0:
				concurrentReqs = 0
			case svcMetrics.ConcurrentRequests > math.MaxInt32:
				concurrentReqs = math.MaxInt32
			default:
				concurrentReqs = int32(svcMetrics.ConcurrentRequests)
			}
			var queueLen int32
			switch {
			case svcMetrics.QueueLength < 0:
				queueLen = 0
			case svcMetrics.QueueLength > math.MaxInt32:
				queueLen = math.MaxInt32
			default:
				queueLen = int32(svcMetrics.QueueLength)
			}
			pbSvcMetrics := &simulationv1.ServiceMetrics{
				ServiceName:             serviceName,
				RequestCount:            svcMetrics.RequestCount,
				ErrorCount:              svcMetrics.ErrorCount,
				LatencyP50Ms:            svcMetrics.LatencyP50,
				LatencyP95Ms:            svcMetrics.LatencyP95,
				LatencyP99Ms:            svcMetrics.LatencyP99,
				LatencyMeanMs:           svcMetrics.LatencyMean,
				CpuUtilization:          svcMetrics.CPUUtilization,
				MemoryUtilization:       svcMetrics.MemoryUtilization,
				ActiveReplicas:          activeReplicas,
				ConcurrentRequests:      concurrentReqs,
				QueueLength:             queueLen,
				QueueWaitP50Ms:          svcMetrics.QueueWaitP50Ms,
				QueueWaitP95Ms:          svcMetrics.QueueWaitP95Ms,
				QueueWaitP99Ms:          svcMetrics.QueueWaitP99Ms,
				QueueWaitMeanMs:         svcMetrics.QueueWaitMeanMs,
				ProcessingLatencyP50Ms:  svcMetrics.ProcessingLatencyP50Ms,
				ProcessingLatencyP95Ms:  svcMetrics.ProcessingLatencyP95Ms,
				ProcessingLatencyP99Ms:  svcMetrics.ProcessingLatencyP99Ms,
				ProcessingLatencyMeanMs: svcMetrics.ProcessingLatencyMeanMs,
			}
			pbMetrics.ServiceMetrics = append(pbMetrics.ServiceMetrics, pbSvcMetrics)
		}
	}

	if engineMetrics.HostMetrics != nil {
		for _, hm := range engineMetrics.HostMetrics {
			if hm == nil {
				continue
			}
			pbMetrics.HostMetrics = append(pbMetrics.HostMetrics, &simulationv1.HostMetrics{
				HostId:            hm.HostID,
				CpuUtilization:    hm.CPUUtilization,
				MemoryUtilization: hm.MemoryUtilization,
			})
		}
	}
	if len(engineMetrics.EndpointRequestStats) > 0 {
		pbMetrics.EndpointRequestStats = make([]*simulationv1.EndpointRequestStats, 0, len(engineMetrics.EndpointRequestStats))
		for _, e := range engineMetrics.EndpointRequestStats {
			row := &simulationv1.EndpointRequestStats{
				ServiceName:             e.ServiceName,
				EndpointPath:            e.EndpointPath,
				RequestCount:            e.RequestCount,
				ErrorCount:              e.ErrorCount,
				LatencyP50Ms:            e.LatencyP50Ms,
				LatencyP95Ms:            e.LatencyP95Ms,
				LatencyP99Ms:            e.LatencyP99Ms,
				LatencyMeanMs:           e.LatencyMeanMs,
				RootLatencyP50Ms:        e.RootLatencyP50Ms,
				RootLatencyP95Ms:        e.RootLatencyP95Ms,
				RootLatencyP99Ms:        e.RootLatencyP99Ms,
				RootLatencyMeanMs:       e.RootLatencyMeanMs,
				QueueWaitP50Ms:          e.QueueWaitP50Ms,
				QueueWaitP95Ms:          e.QueueWaitP95Ms,
				QueueWaitP99Ms:          e.QueueWaitP99Ms,
				QueueWaitMeanMs:         e.QueueWaitMeanMs,
				ProcessingLatencyP50Ms:  e.ProcessingLatencyP50Ms,
				ProcessingLatencyP95Ms:  e.ProcessingLatencyP95Ms,
				ProcessingLatencyP99Ms:  e.ProcessingLatencyP99Ms,
				ProcessingLatencyMeanMs: e.ProcessingLatencyMeanMs,
			}
			pbMetrics.EndpointRequestStats = append(pbMetrics.EndpointRequestStats, row)
		}
	}
	if len(engineMetrics.InstanceRouteStats) > 0 {
		pbMetrics.InstanceRouteStats = make([]*simulationv1.InstanceRouteStats, 0, len(engineMetrics.InstanceRouteStats))
		for _, rs := range engineMetrics.InstanceRouteStats {
			pbMetrics.InstanceRouteStats = append(pbMetrics.InstanceRouteStats, &simulationv1.InstanceRouteStats{
				ServiceName:    rs.ServiceName,
				EndpointPath:   rs.EndpointPath,
				InstanceId:     rs.InstanceID,
				Strategy:       rs.Strategy,
				SelectionCount: rs.SelectionCount,
			})
		}
	}

	return pbMetrics
}
