package simd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
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
	optSafety OptimizationSafetyLimits
	limitsErr error

	optimizationRunner OptimizationRunner // optional; when set, optimization runs use it

	mu                     sync.Mutex
	cancels                map[string]context.CancelFunc
	workloadStates         map[string]*WorkloadState    // key: runID
	resourceManagers       map[string]*resource.Manager // key: runID; for dynamic replica updates
	policyManagers         map[string]*policy.Manager   // key: runID; for dynamic policy updates
	onlineCompletionReason map[string]string            // pending COMPLETED reason for online lease limits
	onlineLeaseDeadline    map[string]time.Time         // wall-clock heartbeat deadline per run
	// onlineCtrlMutationEpoch increments on user-driven runtime mutations so the online
	// controller resets convergence / idle-noop tracking without stopping the run.
	onlineCtrlMutationEpoch map[string]uint64
	onlineCtrlIdle          map[string]onlineCtrlIdleState
	// runScenarios holds the parsed scenario per active run for configuration/metadata export.
	runScenarios map[string]*config.Scenario
	progress     map[string]*RunProgress
}

// onlineCtrlIdleState mirrors controller idle streak metadata into RunProgress JSON for online runs.
type onlineCtrlIdleState struct {
	Stable     bool
	NoopStreak int32
}

type RunProgress struct {
	RunID                          string    `json:"run_id"`
	Status                         string    `json:"status"`
	Mode                           string    `json:"mode"`
	Realtime                       bool      `json:"realtime"`
	IsOptimization                 bool      `json:"is_optimization"`
	StartedAt                      time.Time `json:"started_at"`
	LastProgressAt                 time.Time `json:"last_progress_at"`
	CurrentSimTime                 time.Time `json:"current_sim_time"`
	RequestedDurationMs            int64     `json:"requested_duration_ms"`
	PercentComplete                float64   `json:"percent_complete"`
	EventsScheduled                int64     `json:"events_scheduled"`
	EventsProcessed                int64     `json:"events_processed"`
	CurrentEventQueueLength        int       `json:"current_event_queue_length"`
	MaxEventQueueLengthSeen        int64     `json:"max_event_queue_length_seen"`
	ActiveRequestCount             int       `json:"active_request_count"`
	TotalRequestCount              int64     `json:"total_request_count"`
	RetainedCompletedRequestTraces int       `json:"retained_completed_request_traces"`
	MetricSeriesCount              int       `json:"metric_series_count"`
	MetricSampleCount              int       `json:"metric_sample_count"`
	GenerationHorizon              string    `json:"generation_horizon,omitempty"`
	OnlineControllerStable         bool      `json:"online_controller_stable"`
	OnlineControllerNoopIntervals  int32     `json:"online_controller_noop_intervals"`
	CancellationReason             string    `json:"cancellation_reason,omitempty"`
	LastError                      string    `json:"last_error,omitempty"`
	MemoryAllocBytes               uint64    `json:"memory_alloc_bytes"`
	GoroutineCount                 int       `json:"goroutine_count"`
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
		optSafety:              optimizationSafetyLimitsFromEnv(),
		limitsErr:              limitsErr,
		cancels:                make(map[string]context.CancelFunc),
		workloadStates:         make(map[string]*WorkloadState),
		resourceManagers:       make(map[string]*resource.Manager),
		policyManagers:         make(map[string]*policy.Manager),
		onlineCompletionReason: make(map[string]string),
		onlineLeaseDeadline:    make(map[string]time.Time),
		runScenarios:           make(map[string]*config.Scenario),
		progress:               make(map[string]*RunProgress),
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
	if err := validateOptimizationPreStart(updated.Input, e.optSafety); err != nil {
		if _, serr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, err.Error()); serr != nil {
			logger.Error("failed to set failed status after optimization prestart rejection", "run_id", runID, "error", serr)
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
	delete(e.progress, runID)
	delete(e.onlineCompletionReason, runID)
	delete(e.onlineLeaseDeadline, runID)
	delete(e.onlineCtrlMutationEpoch, runID)
	delete(e.onlineCtrlIdle, runID)
	e.mu.Unlock()
}

func (e *RunExecutor) updateProgress(runID string, p *RunProgress) {
	if p == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.progress[runID] = p
}

func (e *RunExecutor) ActiveProgress() []*RunProgress {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*RunProgress, 0, len(e.progress))
	for _, p := range e.progress {
		cp := *p
		out = append(out, &cp)
	}
	return out
}

func (e *RunExecutor) LimitsSnapshot() SimulationLimits {
	return e.limits
}

func (e *RunExecutor) OptimizationSafetySnapshot() OptimizationSafetyLimits {
	return e.optSafety
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

// NotifyOnlineRuntimeMutation resets online-controller idle/convergence streaks after a
// user-driven workload, replica/resource, or policy change so scaling logic can react again.
func (e *RunExecutor) NotifyOnlineRuntimeMutation(runID string) {
	if runID == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.onlineCtrlMutationEpoch == nil {
		e.onlineCtrlMutationEpoch = make(map[string]uint64)
	}
	e.onlineCtrlMutationEpoch[runID]++
}

func (e *RunExecutor) setOnlineControllerIdle(runID string, stable bool, noopStreak int32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.onlineCtrlIdle == nil {
		e.onlineCtrlIdle = make(map[string]onlineCtrlIdleState)
	}
	e.onlineCtrlIdle[runID] = onlineCtrlIdleState{Stable: stable, NoopStreak: noopStreak}
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
		for i := range topicSnaps {
			t := &topicSnaps[i]
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
	if e.optSafety.MaxWallClockRuntime > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.optSafety.MaxWallClockRuntime)
		defer cancel()
	}

	rec, ok := e.store.Get(runID)
	if !ok {
		logger.Error("run not found", "run_id", runID)
		return
	}
	if rec.Input == nil {
		logger.Info("run input unavailable; skipping execution", "run_id", runID, "status", rec.Run.Status.String())
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
		durationMs = e.optSafety.DefaultEvaluationDuration
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
	e.store.TrimOptimizationCandidates(runID, bestRunID)

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

// applyOnlineOptimizationFinalResult sets best_run_id, best_score, iterations, and candidate_run_ids for a
// single-run online optimization: preserves live progress score/iteration from SetOptimizationProgress, uses
// the larger of progress iterations vs optimization history length, and keeps candidates pointing at this run.
func (e *RunExecutor) applyOnlineOptimizationFinalResult(runID string) error {
	rec, ok := e.store.Get(runID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	bestScore := rec.Run.BestScore
	histLen := len(rec.OptimizationHistory)
	var histSteps int32
	switch {
	case histLen > math.MaxInt32:
		histSteps = math.MaxInt32
	default:
		histSteps = int32(histLen)
	}
	iterations := rec.Run.Iterations
	if histSteps > iterations {
		iterations = histSteps
	}
	return e.store.SetOptimizationResult(runID, runID, bestScore, iterations, []string{runID})
}

// finalizeOnlineOptimizationRun aggregates metrics and marks the run COMPLETED with an optional online_completion_reason.
// simDuration, when positive, overrides aggregate and ingress throughput to use simulated time (not wall-clock collector duration).
func (e *RunExecutor) finalizeOnlineOptimizationRun(runID string, scenario *config.Scenario, rm *resource.Manager, metricsCollector *metrics.Collector, onlineReason string, simDuration time.Duration) {
	metricsCollector.Stop()
	serviceLabels := make([]map[string]string, 0, len(scenario.Services))
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		serviceLabels = append(serviceLabels, metrics.CreateServiceLabels(svc.ID))
	}
	engineMetrics := metrics.ConvertToRunMetrics(metricsCollector, serviceLabels, e.runMetricsOptsForRun(runID))
	attachHostMetrics(scenario, rm, engineMetrics, metricsCollector)
	applyThroughputFromSimDuration(engineMetrics, simDuration)
	for i := range scenario.Services {
		svc := &scenario.Services[i]
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
	if err := e.applyOnlineOptimizationFinalResult(runID); err != nil {
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
	if rec.Input == nil {
		logger.Info("run input unavailable; skipping execution", "run_id", runID, "status", rec.Run.Status.String())
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
	eng.GetRunManager().SetMaxTotalRequests(e.limits.MaxTotalRequests, func(currentCount, max int) {
		eng.TriggerLimitExceeded(&engine.LimitExceededError{
			Limit: "max_total_requests",
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
	metricsCollector.SetLimitCallback(func(limit string, currentCount, max int) {
		eng.TriggerLimitExceeded(&engine.LimitExceededError{
			Limit: limit,
			Value: int64(currentCount),
			Max:   int64(max),
		})
	})
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
		// If a graceful online completion reason was already signaled (e.g. heartbeat
		// expiry), finalize as COMPLETED even if engine stop races context cancellation.
		if reason := e.takeOnlineCompletionReason(runID); reason != "" {
			e.finalizeOnlineOptimizationRun(runID, scenario, rm, metricsCollector, reason, eng.GetSimTime().Sub(startTime))
			return
		}
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
				for i := range scenario.Services {
					svc := &scenario.Services[i]
					serviceLabels = append(serviceLabels, metrics.CreateServiceLabels(svc.ID))
				}
				engineMetrics := metrics.ConvertToRunMetrics(metricsCollector, serviceLabels, e.runMetricsOptsForRun(runID))
				attachHostMetrics(scenario, rm, engineMetrics, metricsCollector)
				applyThroughputFromSimDuration(engineMetrics, eng.GetSimTime().Sub(startTime))
				for i := range scenario.Services {
					svc := &scenario.Services[i]
					if sm := engineMetrics.ServiceMetrics[svc.ID]; sm != nil {
						sm.ActiveReplicas = rm.ActiveReplicas(svc.ID)
					}
				}

				pbMetrics := convertMetricsToProto(engineMetrics)
				if err := e.store.SetMetrics(runID, pbMetrics); err != nil {
					logger.Error("failed to set metrics for stopped online run", "run_id", runID, "error", err)
				}

				// Set optimization result so callback includes best_run_id and top_candidates (self).
				if err := e.applyOnlineOptimizationFinalResult(runID); err != nil {
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
	if rec.Input == nil {
		logger.Info("run input unavailable; skipping execution", "run_id", runID, "status", rec.Run.Status.String())
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
	eng.GetRunManager().SetMaxTotalRequests(e.limits.MaxTotalRequests, func(currentCount, max int) {
		eng.TriggerLimitExceeded(&engine.LimitExceededError{
			Limit: "max_total_requests",
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
	metricsCollector.SetLimitCallback(func(limit string, currentCount, max int) {
		eng.TriggerLimitExceeded(&engine.LimitExceededError{
			Limit: limit,
			Value: int64(currentCount),
			Max:   int64(max),
		})
	})
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
	progressCtx, progressCancel := context.WithCancel(ctx)
	defer progressCancel()
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-progressCtx.Done():
				return
			case <-t.C:
				p := e.buildRunProgress(runID, rec, startTime, duration, eng, metricsCollector, workloadState, "")
				e.updateProgress(runID, p)
				logger.Info("Run progress",
					"run_id", p.RunID,
					"sim_time", p.CurrentSimTime,
					"percent_complete", p.PercentComplete,
					"events_processed", p.EventsProcessed,
					"queue_length", p.CurrentEventQueueLength,
					"active_requests", p.ActiveRequestCount,
					"metric_series", p.MetricSeriesCount,
					"memory_alloc_bytes", p.MemoryAllocBytes,
					"goroutines", p.GoroutineCount,
				)
			}
		}
	}()
	if err := eng.Run(duration); err != nil {
		// Check if it was cancelled
		if ctx.Err() != nil {
			logger.Info("simulation cancelled", "run_id", runID)
			p := e.buildRunProgress(runID, rec, startTime, duration, eng, metricsCollector, workloadState, "cancelled")
			e.updateProgress(runID, p)
			rec, _ := e.store.Get(runID)
			e.sendNotificationIfConfigured(rec)
			return
		}
		logger.Error("simulation failed", "run_id", runID, "error", err)
		p := e.buildRunProgress(runID, rec, startTime, duration, eng, metricsCollector, workloadState, err.Error())
		e.updateProgress(runID, p)
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
	p := e.buildRunProgress(runID, rec, startTime, duration, eng, metricsCollector, workloadState, "")
	e.updateProgress(runID, p)

	// Stop metrics collection
	metricsCollector.Stop()

	// Build service labels for metrics conversion
	serviceLabels := make([]map[string]string, 0)
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		serviceLabels = append(serviceLabels, metrics.CreateServiceLabels(svc.ID))
	}

	// Convert metrics collector data to RunMetrics
	engineMetrics := metrics.ConvertToRunMetrics(metricsCollector, serviceLabels, e.runMetricsOptsForRun(runID))
	attachHostMetrics(scenario, rm, engineMetrics, metricsCollector)
	// For completed runs, use simulation duration for throughput so non-real-time
	// mode reports requests over simulated time instead of wall-clock execution time.
	applyThroughputFromSimDuration(engineMetrics, simDuration)

	// Populate ActiveReplicas from the resource manager (live routable count)
	for i := range scenario.Services {
		svc := &scenario.Services[i]
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
		// For ordinary (non-optimization) runs, expose the run itself as the single
		// candidate/result run for API-contract consistency with optimization flows.
		if rec.Input == nil || rec.Input.Optimization == nil {
			if err := e.store.SetOptimizationResult(runID, runID, 0, 0, []string{runID}); err != nil {
				logger.Error("failed to set self optimization result for ordinary run", "run_id", runID, "error", err)
			}
		}
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

func (e *RunExecutor) buildRunProgress(runID string, rec *RunRecord, startTime time.Time, requested time.Duration, eng *engine.Engine, collector *metrics.Collector, ws *WorkloadState, reason string) *RunProgress {
	if rec == nil {
		rec, _ = e.store.Get(runID)
	}
	snap := eng.ProgressSnapshot(startTime.Add(requested))
	cs := collector.Snapshot()
	rm := eng.GetRunManager().Snapshot()
	percent := 0.0
	if requested > 0 {
		percent = 100 * float64(snap.SimTime.Sub(startTime)) / float64(requested)
		if percent < 0 {
			percent = 0
		}
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	mode := "standard"
	realtime := rec != nil && rec.Input != nil && rec.Input.RealTimeMode
	if realtime {
		mode = "realtime"
	}
	horizon := ""
	if ws != nil {
		h := ws.GeneratedHorizon()
		if !h.IsZero() {
			horizon = h.Format(time.RFC3339Nano)
		}
	}
	status := ""
	isOpt := false
	onlineOpt := false
	if rec != nil && rec.Run != nil {
		status = rec.Run.Status.String()
		isOpt = rec.Input != nil && rec.Input.Optimization != nil
		onlineOpt = rec.Input != nil && rec.Input.GetOptimization() != nil && rec.Input.GetOptimization().GetOnline()
	}
	var ctrlStable bool
	var ctrlNoop int32
	if onlineOpt {
		e.mu.Lock()
		if st, ok := e.onlineCtrlIdle[runID]; ok {
			ctrlStable = st.Stable
			ctrlNoop = st.NoopStreak
		}
		e.mu.Unlock()
	}
	return &RunProgress{
		RunID:                          runID,
		Status:                         status,
		Mode:                           mode,
		Realtime:                       realtime,
		IsOptimization:                 isOpt,
		StartedAt:                      startTime,
		LastProgressAt:                 time.Now(),
		CurrentSimTime:                 snap.SimTime,
		RequestedDurationMs:            int64(requested / time.Millisecond),
		PercentComplete:                percent,
		EventsScheduled:                snap.EventsScheduled,
		EventsProcessed:                snap.EventsProcessed,
		CurrentEventQueueLength:        snap.QueueLength,
		MaxEventQueueLengthSeen:        snap.MaxQueueSeen,
		ActiveRequestCount:             rm.ActiveRequests,
		TotalRequestCount:              rm.TotalRequests,
		RetainedCompletedRequestTraces: rm.RetainedCompletedSamples,
		MetricSeriesCount:              cs.SeriesCount,
		MetricSampleCount:              cs.TotalPoints,
		GenerationHorizon:              horizon,
		OnlineControllerStable:         ctrlStable,
		OnlineControllerNoopIntervals:  ctrlNoop,
		CancellationReason:             reason,
		LastError:                      snap.LastError,
		MemoryAllocBytes:               mem.Alloc,
		GoroutineCount:                 runtime.NumGoroutine(),
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
	topicSnaps := rm.TopicBrokerHealthSnapshots(now)
	for i := range topicSnaps {
		t := &topicSnaps[i]
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

// cpuPrimaryScaleDownSafe reports whether replica or vertical CPU scale-down is allowed for a
// service under CPU-primary policy: P95 within guard band, no broker backlog for the service,
// no local queue depth, bounded concurrency, and non-worsening error rate vs the prior tick.
func cpuPrimaryScaleDownSafe(runMetrics *models.RunMetrics, currentP95, targetP95 float64, p95Guard bool, prevErrFrac float64, brokerPressure map[string]brokerPressureSignal, serviceID string, rm *resource.Manager) bool {
	if runMetrics == nil {
		return false
	}
	if p95Guard && currentP95 > targetP95*1.05 {
		return false
	}
	if p, ok := brokerPressure[serviceID]; ok && (p.HasBacklog || p.HasInFlight || p.MaxOldestAgeMs > 0 || p.HasDrops || p.HasDLQ) {
		return false
	}
	if sm := runMetrics.ServiceMetrics[serviceID]; sm != nil && sm.ConcurrentRequests > 10 {
		return false
	}
	if rm != nil && serviceQueueDepthTotal(rm, serviceID) > 0 {
		return false
	}
	tot := float64(runMetrics.TotalRequests)
	if tot > 0 && prevErrFrac >= 0 {
		curr := float64(runMetrics.FailedRequests) / tot
		if curr > prevErrFrac+0.005 {
			return false
		}
	}
	return true
}

// cpuPrimaryHostScaleInSafe reports whether cluster-level host scale-in is allowed under CPU-primary:
// P95 within guard band, no broker pressure, and non-worsening aggregate error rate.
func cpuPrimaryHostScaleInSafe(runMetrics *models.RunMetrics, currentP95, targetP95 float64, p95Guard bool, prevErrFrac float64, brokerPressure map[string]brokerPressureSignal) bool {
	if runMetrics == nil {
		return false
	}
	if p95Guard && currentP95 > targetP95*1.05 {
		return false
	}
	if onlineAnyBrokerPressure(brokerPressure) {
		return false
	}
	tot := float64(runMetrics.TotalRequests)
	if tot > 0 && prevErrFrac >= 0 {
		curr := float64(runMetrics.FailedRequests) / tot
		if curr > prevErrFrac+0.005 {
			return false
		}
	}
	return true
}

// onlineTopologyGuard is a compatibility helper for topology-aware scale-down checks.
// It returns a stable reason string so tests and audit messages can assert decisions.
func onlineTopologyGuard(runMetrics *models.RunMetrics, _ *config.Scenario, _ string, _ *resource.Manager, opt *simulationv1.OptimizationConfig, _ int) (guarded bool, reason string) {
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

// onlineOptimizationStepMeta describes the controller action for optimization_history replay (Phase 5+).
type onlineOptimizationStepMeta struct {
	Action                 string
	DecisionServiceID      string
	DecisionMetric         string
	DecisionMetricValue    float64
	ObjectiveScoreOverride *float64 // when non-nil, sets objective_score instead of deriving from tick + primary
}

// onlineOptimizationHistoryBundle carries replay context for one recorded online step.
type onlineOptimizationHistoryBundle struct {
	Opt              *simulationv1.OptimizationConfig
	Tick             *onlineCtrlTickInput
	PrimaryTargetCtl string
	Meta             *onlineOptimizationStepMeta
}

func normalizeOnlineHistoryPrimaryTarget(primary string) string {
	p := strings.ToLower(strings.TrimSpace(primary))
	switch p {
	case "cpu_utilization", "memory_utilization":
		return p
	default:
		return "p95_latency"
	}
}

func derivedOnlineObjectiveScore(primaryNorm string, tick *onlineCtrlTickInput, scoreP95 float64) float64 {
	if tick == nil {
		return scoreP95
	}
	switch primaryNorm {
	case "cpu_utilization":
		if tick.runMetrics != nil {
			return maxServiceUtilization(tick.runMetrics, "cpu")
		}
	case "memory_utilization":
		if tick.runMetrics != nil {
			return maxServiceUtilization(tick.runMetrics, "memory")
		}
	default:
		return tick.currentP95
	}
	return scoreP95
}

func fillOptimizationStepReplay(step *simulationv1.OptimizationStep, bundle *onlineOptimizationHistoryBundle, scoreP95 float64) {
	if step == nil || bundle == nil || bundle.Meta == nil || bundle.Tick == nil {
		return
	}
	opt := bundle.Opt
	tick := bundle.Tick
	meta := bundle.Meta
	primaryNorm := normalizeOnlineHistoryPrimaryTarget(bundle.PrimaryTargetCtl)

	step.PrimaryTarget = primaryNorm
	step.Action = meta.Action
	step.DecisionServiceId = meta.DecisionServiceID
	step.DecisionMetric = meta.DecisionMetric
	step.DecisionMetricValue = meta.DecisionMetricValue

	switch primaryNorm {
	case "cpu_utilization", "memory_utilization":
		if opt != nil {
			step.TargetUtilLow = EffectiveOnlineTargetUtilLow(opt)
			step.TargetUtilHigh = EffectiveOnlineTargetUtilHigh(opt)
		}
		step.ObjectiveUnit = "ratio"
	default:
		step.ObjectiveUnit = "ms"
	}

	if meta.ObjectiveScoreOverride != nil {
		step.ObjectiveScore = *meta.ObjectiveScoreOverride
	} else {
		step.ObjectiveScore = derivedOnlineObjectiveScore(primaryNorm, tick, scoreP95)
	}

	if opt != nil && opt.GetTargetP95LatencyMs() > 0 {
		step.GuardrailP95Ms = opt.GetTargetP95LatencyMs()
	}
	step.CurrentP95Ms = tick.currentP95

	if tick.runMetrics != nil {
		tot := tick.runMetrics.TotalRequests
		if tot > 0 {
			er := float64(tick.runMetrics.FailedRequests) / float64(tot)
			step.CurrentErrorRate = &er
		}
	}
}

// recordOptimizationStep appends an optimization step to the run's history for backend persistence.
// When bundle is non-nil with Meta set, Phase 5 replay fields (primary_target, objective_*, guardrails, action, …) are populated.
func (e *RunExecutor) recordOptimizationStep(runID string, iterationIndex int32, targetP95, scoreP95 float64, reason string, prevConfig, currConfig *simulationv1.RunConfiguration, bundle *onlineOptimizationHistoryBundle) {
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
	if bundle != nil && bundle.Meta != nil {
		fillOptimizationStepReplay(step, bundle, scoreP95)
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
	if err := validateOnlinePrimaryUtilizationBand(opt, primary); err != nil {
		return err
	}
	minHC := opt.GetMinHostCpuCores()
	maxHC := opt.GetMaxHostCpuCores()
	if minHC > 0 && maxHC > 0 && minHC > maxHC {
		return fmt.Errorf("online optimization: min_host_cpu_cores (%d) must be <= max_host_cpu_cores (%d)", minHC, maxHC)
	}
	minHM := opt.GetMinHostMemoryGb()
	maxHM := opt.GetMaxHostMemoryGb()
	if minHM > 0 && maxHM > 0 && minHM > maxHM {
		return fmt.Errorf("online optimization: min_host_memory_gb (%d) must be <= max_host_memory_gb (%d)", minHM, maxHM)
	}
	if opt.GetHostCpuStepCores() < 0 || opt.GetHostMemoryStepGb() < 0 {
		return fmt.Errorf("online optimization: host_cpu_step_cores and host_memory_step_gb must be non-negative")
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
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		serviceLabels = append(serviceLabels, metrics.CreateServiceLabels(svc.ID))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	bestScore := math.Inf(1)
	var iter int32

	const (
		cpuHighThreshold     = 0.8 // above this, consider service CPU "hot"
		hostCPUHighThreshold = 0.8 // above this, consider host CPU "hot"
	)
	scaleDownCPUMax := opt.GetScaleDownCpuUtilMax()
	scaleDownMemMax := opt.GetScaleDownMemUtilMax()

	// Host scaling bounds. Defaults: use the initial scenario host count as both
	// the minimum and maximum when not explicitly configured.
	initialHosts := len(scenario.Hosts)
	minHosts, maxHosts := onlineEffectiveMinMaxHosts(opt, initialHosts)
	if primaryTargetCtl == "cpu_utilization" {
		for _, w := range onlineCPUPrimaryHostScalingWarnings(opt, initialHosts) {
			logger.Warn(w, "run_id", runID, "initial_hosts", initialHosts, "effective_min_hosts", minHosts, "effective_max_hosts", maxHosts)
		}
	}
	for _, w := range onlineHostVerticalCapacityWarnings(opt, scenario) {
		logger.Warn(w, "run_id", runID)
	}
	scaleDownHostCPUMax := opt.GetScaleDownHostCpuUtilMax()
	initialHostCores := ScenarioInitialHostCPUCores(scenario)
	initialHostMemGB := ScenarioInitialHostMemoryGB(scenario)
	minHostCPUFloor := EffectiveOnlineMinHostCPUCores(opt, initialHostCores)
	maxHostCPUCap := EffectiveOnlineMaxHostCPUCores(opt, initialHostCores)
	minHostMemFloor := EffectiveOnlineMinHostMemoryGb(opt, initialHostMemGB)
	maxHostMemCap := EffectiveOnlineMaxHostMemoryGb(opt, initialHostMemGB)
	hostCPUStepCores := EffectiveOnlineHostCPUStepCores(opt)
	hostMemStepGB := EffectiveOnlineHostMemoryStepGb(opt)

	loopState := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1.0,
	}
	intervalMs := int64(interval / time.Millisecond)
	if intervalMs < 1 {
		intervalMs = 1
	}
	var noopStreak int32
	maxStepsNoticeLogged := false
	var mutationEpochSeen uint64
	idleStableLogged := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.mu.Lock()
			epoch := uint64(0)
			if e.onlineCtrlMutationEpoch != nil {
				epoch = e.onlineCtrlMutationEpoch[runID]
			}
			e.mu.Unlock()
			if epoch != mutationEpochSeen {
				mutationEpochSeen = epoch
				noopStreak = 0
				idleStableLogged = false
				e.setOnlineControllerIdle(runID, false, 0)
			}

			stepIndexBefore := loopState.stepIndex
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
			if opt.GetMaxControllerSteps() > 0 && loopState.stepIndex >= opt.GetMaxControllerSteps() {
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
			if cooldown > 0 && !loopState.lastScaleWall.IsZero() && time.Since(loopState.lastScaleWall) < cooldown {
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

			hostCount := rm.HostCount()
			maxHostCPU := rm.MaxHostCPUUtilization()
			maxHostMem := rm.MaxHostMemoryUtilization()

			tickIn := &onlineCtrlTickInput{
				runMetrics:           runMetrics,
				currentP95:           currentP95,
				targetP95:            targetP95,
				p95Guard:             p95Guard,
				brokerPressure:       brokerPressure,
				hostCount:            hostCount,
				maxHostCPU:           maxHostCPU,
				maxHostMem:           maxHostMem,
				stabTicks:            stabTicks,
				minReplicasCtl:       minReplicasCtl,
				minCPUCtl:            minCPUCtl,
				minMemCtl:            minMemCtl,
				memHeadroomCtl:       memHeadroomCtl,
				scaleDownCPUMax:      scaleDownCPUMax,
				scaleDownMemMax:      scaleDownMemMax,
				scaleDownHostCPUMax:  scaleDownHostCPUMax,
				scaleDownHostMemMax:  scaleDownHostMemMax,
				initialHostCores:     initialHostCores,
				initialHostMemGB:     initialHostMemGB,
				minHosts:             minHosts,
				maxHosts:             maxHosts,
				cpuHighThreshold:     cpuHighThreshold,
				hostCPUHighThreshold: hostCPUHighThreshold,
				minHostCPUCores:      minHostCPUFloor,
				maxHostCPUCores:      maxHostCPUCap,
				minHostMemGB:         minHostMemFloor,
				maxHostMemGB:         maxHostMemCap,
				hostCPUStepCores:     hostCPUStepCores,
				hostMemoryStepGB:     hostMemStepGB,
			}

			switch onlineControllerPolicyBranchFromPrimary(primaryTargetCtl) {
			case onlinePolicyCPU:
				e.runCPUPrimaryOnlineStep(runID, scenario, opt, rm, state, loopState, tickIn, primaryTargetCtl)
			case onlinePolicyMemory:
				e.runMemoryPrimaryOnlineStep(runID, scenario, opt, rm, state, loopState, tickIn, primaryTargetCtl)
			default:
				e.runP95PrimaryOnlineStep(runID, scenario, opt, rm, state, loopState, tickIn, primaryTargetCtl)
			}

			if loopState.stepIndex > stepIndexBefore {
				loopState.lastScaleWall = time.Now()
			}
			if runMetrics.TotalRequests > 0 {
				loopState.prevErrFrac = float64(runMetrics.FailedRequests) / float64(runMetrics.TotalRequests)
			}

			maxNoop := opt.GetMaxNoopIntervals()
			if maxNoop > 0 {
				if loopState.stepIndex == stepIndexBefore {
					noopStreak++
					if noopStreak >= maxNoop {
						logger.Info("online controller converged (no configuration changes)",
							"run_id", runID, "noop_intervals", noopStreak)
						e.signalOnlineLeaseEnd(runID, OnlineCompletionConverged)
					}
				} else {
					noopStreak = 0
				}
			} else if maxNoop < 0 {
				// Interactive / no convergence stop: track idle streak for progress metadata only.
				const minIntervalsStable = 2
				if loopState.stepIndex == stepIndexBefore {
					noopStreak++
					stable := noopStreak >= minIntervalsStable
					e.setOnlineControllerIdle(runID, stable, noopStreak)
					if stable && !idleStableLogged {
						logger.Info("online controller stable (no configuration changes; interactive session continues)",
							"run_id", runID, "noop_intervals", noopStreak)
						idleStableLogged = true
					}
				} else {
					noopStreak = 0
					idleStableLogged = false
					e.setOnlineControllerIdle(runID, false, 0)
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
		for i := range engineMetrics.EndpointRequestStats {
			e := &engineMetrics.EndpointRequestStats[i]
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
