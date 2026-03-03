package simd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

type mockOptimizationRunner struct{}

func (m *mockOptimizationRunner) RunExperiment(ctx context.Context, runID string, scenario *config.Scenario, durationMs int64, params *OptimizationParams) (string, float64, int32, []string, error) {
	return "best", 0.0, 0, nil, nil
}

func TestRunExecutorStartTransitionsToRunning(t *testing.T) {
	store := NewRunStore()
	validScenario := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`
	_, err := store.Create("run-1", &simulationv1.RunInput{
		ScenarioYaml: validScenario,
		DurationMs:   50, // Short duration for test
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	rec, err := exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Fatalf("expected running, got %v", rec.Run.Status)
	}

	// Wait for completion (poll with timeout)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rec, ok := store.Get("run-1")
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("expected completed, got %v", rec.Run.Status)
	}
}

func TestRunExecutorSetOptimizationRunner(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store)
	mock := &mockOptimizationRunner{}
	exec.SetOptimizationRunner(mock)
	// No assertion needed - just ensure it doesn't panic
}

func TestRunExecutorStartEmptyRunID(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store)
	_, err := exec.Start("")
	if err == nil {
		t.Fatalf("expected error for empty run ID")
	}
	if !errors.Is(err, ErrRunIDMissing) {
		t.Fatalf("expected ErrRunIDMissing, got %v", err)
	}
}

func TestRunExecutorStopEmptyRunID(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store)
	_, err := exec.Stop("")
	if err == nil {
		t.Fatalf("expected error for empty run ID")
	}
	if !errors.Is(err, ErrRunIDMissing) {
		t.Fatalf("expected ErrRunIDMissing, got %v", err)
	}
}

func TestRunExecutorStartOptimizationWithoutRunner(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store)
	optScenario := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`
	rec, err := store.Create("opt-run", &simulationv1.RunInput{
		ScenarioYaml: optScenario,
		DurationMs:   0,
		Optimization: &simulationv1.OptimizationConfig{
			Objective:            "p95_latency_ms",
			MaxIterations:        3,
			EvaluationDurationMs: 5000,
		},
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	_, err = exec.Start(rec.Run.Id)
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	// Wait for optimization to fail (no runner configured)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, ok := store.Get(rec.Run.Id)
		if ok && r.Run.Status == simulationv1.RunStatus_RUN_STATUS_FAILED {
			if r.Run.Error == "" || !strings.Contains(r.Run.Error, "optimization not enabled") {
				t.Fatalf("expected failure with optimization message, got: %s", r.Run.Error)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected run to fail with optimization not configured")
}

// Test that online optimization mode selects the online path without panicking.
func TestRunExecutorStartOnlineOptimizationWithoutRunner(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store)
	optScenario := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`
	rec, err := store.Create("opt-run-online", &simulationv1.RunInput{
		ScenarioYaml: optScenario,
		DurationMs:   0,
		RealTimeMode: true,
		Optimization: &simulationv1.OptimizationConfig{
			Objective:            "p95_latency_ms",
			MaxIterations:        3,
			EvaluationDurationMs: 0,
			Online:               true,
			TargetP95LatencyMs:   50.0,
			ControlIntervalMs:    50,
		},
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	_, err = exec.Start(rec.Run.Id)
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Let the online run start and then stop it.
	time.Sleep(100 * time.Millisecond)
	if _, err := exec.Stop(rec.Run.Id); err != nil {
		t.Fatalf("Stop error: %v", err)
	}
}

// Test that the online controller scales replicas up when p95 latency is above target.
func TestRunExecutorOnlineControllerScalesUp(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())
	runID := "online-scale-up"

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
	}

	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario error: %v", err)
	}

	collector := metrics.NewCollector()
	collector.Start()

	// Record high latency so p95 is well above target.
	now := time.Now()
	for i := 0; i < 5; i++ {
		metrics.RecordLatency(collector, 200.0, now.Add(time.Duration(i)*time.Millisecond), metrics.CreateServiceLabels("svc1"))
	}

	// Make resource manager visible to UpdateServiceReplicas.
	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		TargetP95LatencyMs: 50.0,
		ControlIntervalMs:  10,
		StepSize:           1.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm)

	// Allow a few control iterations.
	time.Sleep(80 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	replicas := rm.ActiveReplicas("svc1")
	if replicas <= 1 {
		t.Fatalf("expected replicas to scale up above 1, got %d", replicas)
	}
}

// Test that the online controller scales replicas down when p95 latency is well below target.
func TestRunExecutorOnlineControllerScalesDown(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())
	runID := "online-scale-down"

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 2}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 3,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
	}

	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario error: %v", err)
	}

	collector := metrics.NewCollector()
	collector.Start()

	// Record low latency so p95 is well below target.
	now := time.Now()
	for i := 0; i < 5; i++ {
		metrics.RecordLatency(collector, 5.0, now.Add(time.Duration(i)*time.Millisecond), metrics.CreateServiceLabels("svc1"))
	}

	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		TargetP95LatencyMs: 100.0,
		ControlIntervalMs:  10,
		StepSize:           1.0,
	}

	initialReplicas := rm.ActiveReplicas("svc1")
	if initialReplicas <= 1 {
		t.Fatalf("expected initial replicas > 1, got %d", initialReplicas)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm)

	time.Sleep(80 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	replicas := rm.ActiveReplicas("svc1")
	if replicas >= initialReplicas {
		t.Fatalf("expected replicas to scale down below %d, got %d", initialReplicas, replicas)
	}
	if replicas < 1 {
		t.Fatalf("expected replicas to stay >= 1, got %d", replicas)
	}
}

// Test that the online controller prefers vertical scaling (CPU cores per instance)
// when latency is above target and service CPU utilization is high.
func TestRunExecutorOnlineControllerPrefersVerticalScaleUpOnHighCPU(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())
	runID := "online-vertical-scale-up"

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 16}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/test",
						MeanCPUMs:    10,
						CPUSigmaMs:   2,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5},
					},
				},
			},
		},
	}

	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario error: %v", err)
	}

	collector := metrics.NewCollector()
	collector.Start()

	// Record high latency so p95 is well above target and high CPU utilization for svc1.
	now := time.Now()
	svcLabels := metrics.CreateServiceLabels("svc1")
	for i := 0; i < 5; i++ {
		ts := now.Add(time.Duration(i) * time.Millisecond)
		metrics.RecordLatency(collector, 200.0, ts, svcLabels)
		metrics.RecordCPUUtilization(collector, 0.9, ts, svcLabels)
	}

	// Make resource manager visible to controller helper methods.
	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		TargetP95LatencyMs: 50.0,
		ControlIntervalMs:  10,
		StepSize:           1.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm)

	// Allow a few control iterations.
	time.Sleep(80 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	instances := rm.GetInstancesForService("svc1")
	if len(instances) == 0 {
		t.Fatalf("expected instances for svc1")
	}
	if instances[0].CPUCores() <= resource.DefaultInstanceCPUCores {
		t.Fatalf("expected CPU cores to scale up above default, got %f", instances[0].CPUCores())
	}
}

func TestRunExecutorCallbackWithInvalidURL(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store)
	scenario := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`
	rec, err := store.Create("run-1", &simulationv1.RunInput{
		ScenarioYaml: scenario,
		DurationMs:   50,
		CallbackUrl:  "http://169.254.169.254/metadata", // Invalid: metadata endpoint
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	_, err = exec.Start(rec.Run.Id)
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		r, ok := store.Get(rec.Run.Id)
		if ok && r.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected run to complete")
}

func TestRunExecutorStartOnMissingRun(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())
	_, err := exec.Start("nope")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRunExecutorStartOnEmptyRunID(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())
	_, err := exec.Start("")
	if err == nil {
		t.Fatalf("expected error")
	}
	if err != ErrRunIDMissing {
		t.Fatalf("expected ErrRunIDMissing, got %v", err)
	}
}

func TestRunExecutorStartOnTerminalStatus(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "hosts: []"})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	_, err = store.SetStatus("run-1", simulationv1.RunStatus_RUN_STATUS_COMPLETED, "")
	if err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err == nil {
		t.Fatalf("expected error for terminal status")
	}
}

func TestRunExecutorStopCancelsRun(t *testing.T) {
	store := NewRunStore()
	validScenario := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`
	_, err := store.Create("run-1", &simulationv1.RunInput{
		ScenarioYaml: validScenario,
		DurationMs:   500, // Short duration for cancellation test
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Stop immediately
	_, err = exec.Stop("run-1")
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}

	// Wait briefly for cancellation to process
	time.Sleep(20 * time.Millisecond)

	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_CANCELLED {
		t.Fatalf("expected cancelled, got %v", rec.Run.Status)
	}
}

func TestRunExecutorStopOnEmptyRunID(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())
	_, err := exec.Stop("")
	if err == nil {
		t.Fatalf("expected error")
	}
	if err != ErrRunIDMissing {
		t.Fatalf("expected ErrRunIDMissing, got %v", err)
	}
}

func TestRunExecutorStopOnNonExistentRun(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())
	_, err := exec.Stop("nope")
	// Stop will error because SetStatus will fail on non-existent run
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestRunExecutorStartTwiceReturnsSameRun(t *testing.T) {
	store := NewRunStore()
	validScenario := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`
	_, err := store.Create("run-1", &simulationv1.RunInput{
		ScenarioYaml: validScenario,
		DurationMs:   100,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	rec1, err := exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Start again should return same run
	rec2, err := exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error on second call: %v", err)
	}
	if rec1.Run.Id != rec2.Run.Id {
		t.Fatalf("expected same run ID")
	}
	if rec2.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Fatalf("expected running status")
	}
}

func TestRunExecutorInvalidScenarioYAML(t *testing.T) {
	store := NewRunStore()
	_, err := store.Create("run-1", &simulationv1.RunInput{
		ScenarioYaml: "invalid: yaml: [",
		DurationMs:   100,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Wait for failure
	// Wait for completion (poll with timeout)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rec, ok := store.Get("run-1")
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_FAILED {
		t.Fatalf("expected failed, got %v", rec.Run.Status)
	}
	if rec.Run.Error == "" {
		t.Fatalf("expected error message")
	}
}

func TestRunExecutorDefaultDuration(t *testing.T) {
	store := NewRunStore()
	validScenario := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`
	_, err := store.Create("run-1", &simulationv1.RunInput{
		ScenarioYaml: validScenario,
		DurationMs:   0, // Zero duration should use default
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Wait a bit to ensure it started
	time.Sleep(50 * time.Millisecond)
	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING && rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("expected running or completed, got %v", rec.Run.Status)
	}
}

func TestRunExecutorContextCancellation(t *testing.T) {
	store := NewRunStore()
	validScenario := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`
	_, err := store.Create("run-1", &simulationv1.RunInput{
		ScenarioYaml: validScenario,
		DurationMs:   500, // Short duration for cancellation test
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Stop via executor
	_, err = exec.Stop("run-1")
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}

	// Wait for cancellation (poll with timeout)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rec, ok := store.Get("run-1")
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_CANCELLED {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_CANCELLED {
		t.Fatalf("expected cancelled, got %v", rec.Run.Status)
	}
}
