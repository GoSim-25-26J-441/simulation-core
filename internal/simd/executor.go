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
)

// RunExecutor manages asynchronous run execution and per-run cancellation.
type RunExecutor struct {
	store *RunStore

	mu             sync.Mutex
	cancels        map[string]context.CancelFunc
	workloadStates map[string]*WorkloadState // key: runID
}

var (
	ErrRunNotFound  = errors.New("run not found")
	ErrRunTerminal  = errors.New("run is terminal")
	ErrRunIDMissing = errors.New("run_id is required")
)

func NewRunExecutor(store *RunStore) *RunExecutor {
	return &RunExecutor{
		store:          store,
		cancels:        make(map[string]context.CancelFunc),
		workloadStates: make(map[string]*WorkloadState),
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
		simulationv1.RunStatus_RUN_STATUS_CANCELLED:
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

	go e.runSimulation(ctx, runID)
	return updated, nil
}

// Stop requests cancellation for a running run and marks it cancelled.
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

	updated, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_CANCELLED, "")
	if err != nil {
		return nil, err
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
	// Stop and remove workload state
	if ws, ok := e.workloadStates[runID]; ok {
		ws.Stop()
		delete(e.workloadStates, runID)
	}
	e.mu.Unlock()
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
		if _, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("invalid scenario: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
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
		if _, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("resource initialization failed: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
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
		if _, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("scenario state creation failed: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
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
		if _, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, fmt.Sprintf("workload state initialization failed: %v", err)); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
		}
		return
	}

	// Store workload state for rate updates
	e.mu.Lock()
	e.workloadStates[runID] = workloadState
	e.mu.Unlock()

	// Run simulation
	logger.Info("starting simulation", "run_id", runID, "duration", duration)
	if err := eng.Run(duration); err != nil {
		// Check if it was cancelled
		if ctx.Err() != nil {
			logger.Info("simulation cancelled", "run_id", runID)
			return
		}
		logger.Error("simulation failed", "run_id", runID, "error", err)
		if _, setErr := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_FAILED, err.Error()); setErr != nil {
			logger.Error("failed to set failed status", "run_id", runID, "error", setErr)
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

	// Convert engine metrics to protobuf format
	pbMetrics := convertMetricsToProto(engineMetrics)

	// Store metrics
	if err := e.store.SetMetrics(runID, pbMetrics); err != nil {
		logger.Error("failed to set metrics", "run_id", runID, "error", err)
	}

	// Mark as completed if still running
	rec, ok = e.store.Get(runID)
	if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_RUNNING {
		if _, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_COMPLETED, ""); err != nil {
			logger.Error("failed to set completed status", "run_id", runID, "error", err)
		} else {
			logger.Info("run completed", "run_id", runID,
				"total_requests", pbMetrics.TotalRequests,
				"throughput_rps", pbMetrics.ThroughputRps)
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
