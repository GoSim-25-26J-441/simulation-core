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

	exec := NewRunExecutor(store, nil)
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

func TestRunExecutorCompletedThroughputUsesSimulationDuration(t *testing.T) {
	store := NewRunStore()
	const durationMs int64 = 1000
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
    arrival: {type: constant, rate_rps: 20}
`
	_, err := store.Create("run-throughput-sim-duration", &simulationv1.RunInput{
		ScenarioYaml: scenario,
		DurationMs:   durationMs,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store, nil)
	if _, err := exec.Start("run-throughput-sim-duration"); err != nil {
		t.Fatalf("Start error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.Get("run-throughput-sim-duration")
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			if rec.Metrics == nil {
				t.Fatalf("expected metrics to be stored on completion")
			}
			expected := float64(rec.Metrics.TotalRequests) / (float64(durationMs) / 1000.0)
			if rec.Metrics.ThroughputRps != expected {
				t.Fatalf("expected throughput %f based on simulation duration, got %f", expected, rec.Metrics.ThroughputRps)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected run to complete within timeout")
}

func TestRunExecutorSetOptimizationRunner(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	mock := &mockOptimizationRunner{}
	exec.SetOptimizationRunner(mock)
	// No assertion needed - just ensure it doesn't panic
}

func TestRunExecutorStartEmptyRunID(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
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
	exec := NewRunExecutor(store, nil)
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
	exec := NewRunExecutor(store, nil)
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
	exec := NewRunExecutor(store, nil)
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
	exec := NewRunExecutor(NewRunStore(), nil)
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
	exec := NewRunExecutor(NewRunStore(), nil)
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

// Test allowScaleDownReplicas: utilization-gated scale-down (Phase 1).
func TestAllowScaleDownReplicas(t *testing.T) {
	tests := []struct {
		name               string
		svcCPUUtil         float64
		svcMemUtil         float64
		scaleDownCPUMax    float64
		scaleDownMemMax    float64
		wantAllowScaleDown bool
	}{
		{
			name:               "both thresholds 0 uses only cpuHigh guard: low CPU allows",
			svcCPUUtil:         0.5,
			svcMemUtil:         0.6,
			scaleDownCPUMax:    0,
			scaleDownMemMax:    0,
			wantAllowScaleDown: true,
		},
		{
			name:               "both thresholds 0: CPU hot blocks",
			svcCPUUtil:         0.9,
			svcMemUtil:         0.3,
			scaleDownCPUMax:    0,
			scaleDownMemMax:    0,
			wantAllowScaleDown: false,
		},
		{
			name:               "scale_down thresholds set: CPU above threshold blocks",
			svcCPUUtil:         0.5,
			svcMemUtil:         0.2,
			scaleDownCPUMax:    0.4,
			scaleDownMemMax:    0.4,
			wantAllowScaleDown: false,
		},
		{
			name:               "scale_down thresholds set: mem above threshold blocks",
			svcCPUUtil:         0.2,
			svcMemUtil:         0.6,
			scaleDownCPUMax:    0.4,
			scaleDownMemMax:    0.4,
			wantAllowScaleDown: false,
		},
		{
			name:               "scale_down thresholds set: both below allows",
			svcCPUUtil:         0.2,
			svcMemUtil:         0.3,
			scaleDownCPUMax:    0.4,
			scaleDownMemMax:    0.4,
			wantAllowScaleDown: true,
		},
		{
			name:               "only CPU threshold set: mem ignored",
			svcCPUUtil:         0.2,
			svcMemUtil:         0.9,
			scaleDownCPUMax:    0.4,
			scaleDownMemMax:    0,
			wantAllowScaleDown: true,
		},
		{
			name:               "CPU at cpuHighThreshold blocks even with low scale-down threshold",
			svcCPUUtil:         0.85,
			svcMemUtil:         0.1,
			scaleDownCPUMax:    0.9,
			scaleDownMemMax:    0.9,
			wantAllowScaleDown: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allowScaleDownReplicas(tt.svcCPUUtil, tt.svcMemUtil, tt.scaleDownCPUMax, tt.scaleDownMemMax)
			if got != tt.wantAllowScaleDown {
				t.Errorf("allowScaleDownReplicas() = %v, want %v (cpu=%.2f mem=%.2f scaleDownCPU=%.2f scaleDownMem=%.2f)",
					got, tt.wantAllowScaleDown, tt.svcCPUUtil, tt.svcMemUtil, tt.scaleDownCPUMax, tt.scaleDownMemMax)
			}
		})
	}
}

// Test that with optimization_target_primary = "cpu_utilization", high util scales up,
// low util + low P95 scales down, and low util but high P95 does not scale down (guardrail).
func TestRunExecutorOnlineControllerCPUUtilizationPrimary(t *testing.T) {
	t.Run("high_util_scales_up", func(t *testing.T) {
		exec := NewRunExecutor(NewRunStore(), nil)
		runID := "online-cpu-primary-scale-up"
		scenario := &config.Scenario{
			Hosts: []config.Host{{ID: "host-1", Cores: 8}},
			Services: []config.Service{
				{
					ID: "svc1", Replicas: 1, Model: "cpu",
					Endpoints: []config.Endpoint{
						{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5}},
					},
				},
			},
		}
		rm := resource.NewManager()
		if err := rm.InitializeFromScenario(scenario); err != nil {
			t.Fatalf("InitializeFromScenario: %v", err)
		}
		collector := metrics.NewCollector()
		collector.Start()
		svcLabels := metrics.CreateServiceLabels("svc1")
		now := time.Now()
		for i := 0; i < 10; i++ {
			metrics.RecordCPUUtilization(collector, 0.85, now.Add(time.Duration(i)*time.Millisecond), svcLabels)
			metrics.RecordLatency(collector, 20.0, now.Add(time.Duration(i)*time.Millisecond), svcLabels)
		}
		exec.mu.Lock()
		exec.resourceManagers[runID] = rm
		exec.mu.Unlock()
		opt := &simulationv1.OptimizationConfig{
			Online:                    true,
			TargetP95LatencyMs:        50.0,
			ControlIntervalMs:         10,
			StepSize:                  1.0,
			OptimizationTargetPrimary: "cpu_utilization",
			TargetUtilHigh:            0.7,
			TargetUtilLow:             0.4,
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm)
		time.Sleep(80 * time.Millisecond)
		cancel()
		time.Sleep(20 * time.Millisecond)
		replicas := rm.ActiveReplicas("svc1")
		instances := rm.GetInstancesForService("svc1")
		scaledUp := replicas > 1
		if !scaledUp && len(instances) > 0 {
			scaledUp = instances[0].CPUCores() > resource.DefaultInstanceCPUCores
		}
		if !scaledUp {
			t.Errorf("expected scale-up (replicas or CPU) when util > target_util_high, got replicas=%d", replicas)
		}
	})
	t.Run("low_util_and_low_p95_scales_down", func(t *testing.T) {
		exec := NewRunExecutor(NewRunStore(), nil)
		runID := "online-cpu-primary-scale-down"
		scenario := &config.Scenario{
			Hosts: []config.Host{{ID: "host-1", Cores: 8}},
			Services: []config.Service{
				{
					ID: "svc1", Replicas: 2, Model: "cpu",
					Endpoints: []config.Endpoint{
						{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5}},
					},
				},
			},
		}
		rm := resource.NewManager()
		if err := rm.InitializeFromScenario(scenario); err != nil {
			t.Fatalf("InitializeFromScenario: %v", err)
		}
		collector := metrics.NewCollector()
		collector.Start()
		svcLabels := metrics.CreateServiceLabels("svc1")
		now := time.Now()
		for i := 0; i < 10; i++ {
			metrics.RecordCPUUtilization(collector, 0.2, now.Add(time.Duration(i)*time.Millisecond), svcLabels)
			metrics.RecordLatency(collector, 5.0, now.Add(time.Duration(i)*time.Millisecond), svcLabels)
		}
		exec.mu.Lock()
		exec.resourceManagers[runID] = rm
		exec.mu.Unlock()
		opt := &simulationv1.OptimizationConfig{
			Online:                    true,
			TargetP95LatencyMs:        100.0,
			ControlIntervalMs:         10,
			StepSize:                  1.0,
			OptimizationTargetPrimary: "cpu_utilization",
			TargetUtilHigh:            0.7,
			TargetUtilLow:             0.4,
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm)
		time.Sleep(80 * time.Millisecond)
		cancel()
		time.Sleep(20 * time.Millisecond)
		replicas := rm.ActiveReplicas("svc1")
		if replicas >= 2 {
			t.Errorf("expected scale-down when util < target_util_low and P95 ok, got replicas=%d", replicas)
		}
		if replicas < 1 {
			t.Errorf("expected replicas >= 1, got %d", replicas)
		}
	})
	t.Run("low_util_but_high_p95_no_scale_down", func(t *testing.T) {
		exec := NewRunExecutor(NewRunStore(), nil)
		runID := "online-cpu-primary-guardrail"
		scenario := &config.Scenario{
			Hosts: []config.Host{{ID: "host-1", Cores: 8}},
			Services: []config.Service{
				{
					ID: "svc1", Replicas: 2, Model: "cpu",
					Endpoints: []config.Endpoint{
						{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5}},
					},
				},
			},
		}
		rm := resource.NewManager()
		if err := rm.InitializeFromScenario(scenario); err != nil {
			t.Fatalf("InitializeFromScenario: %v", err)
		}
		collector := metrics.NewCollector()
		collector.Start()
		svcLabels := metrics.CreateServiceLabels("svc1")
		now := time.Now()
		for i := 0; i < 10; i++ {
			metrics.RecordCPUUtilization(collector, 0.2, now.Add(time.Duration(i)*time.Millisecond), svcLabels)
			metrics.RecordLatency(collector, 200.0, now.Add(time.Duration(i)*time.Millisecond), svcLabels)
		}
		exec.mu.Lock()
		exec.resourceManagers[runID] = rm
		exec.mu.Unlock()
		opt := &simulationv1.OptimizationConfig{
			Online:                    true,
			TargetP95LatencyMs:        50.0,
			ControlIntervalMs:         10,
			StepSize:                  1.0,
			OptimizationTargetPrimary: "cpu_utilization",
			TargetUtilHigh:            0.7,
			TargetUtilLow:             0.4,
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm)
		time.Sleep(80 * time.Millisecond)
		cancel()
		time.Sleep(20 * time.Millisecond)
		replicas := rm.ActiveReplicas("svc1")
		if replicas < 2 {
			t.Errorf("expected no scale-down when P95 above target (guardrail), got replicas=%d", replicas)
		}
	})
	t.Run("progress_reports_cpu_score", func(t *testing.T) {
		store := NewRunStore()
		exec := NewRunExecutor(store, nil)
		runID := "online-cpu-progress"
		input := &simulationv1.RunInput{
			ScenarioYaml: "hosts:\n  - id: host-1\n    cores: 4\nservices:\n  - id: svc1\n    replicas: 1\n    model: cpu\n",
			DurationMs:   0,
			Optimization: &simulationv1.OptimizationConfig{
				Online:                    true,
				TargetP95LatencyMs:        50.0,
				ControlIntervalMs:         15,
				OptimizationTargetPrimary: "cpu_utilization",
			},
		}
		_, err := store.Create(runID, input)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		scenario := &config.Scenario{
			Hosts: []config.Host{{ID: "host-1", Cores: 4}},
			Services: []config.Service{
				{ID: "svc1", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2}}},
			},
		}
		rm := resource.NewManager()
		if err := rm.InitializeFromScenario(scenario); err != nil {
			t.Fatalf("InitializeFromScenario: %v", err)
		}
		collector := metrics.NewCollector()
		collector.Start()
		svcLabels := metrics.CreateServiceLabels("svc1")
		now := time.Now()
		for i := 0; i < 20; i++ {
			metrics.RecordCPUUtilization(collector, 0.35, now.Add(time.Duration(i)*time.Millisecond), svcLabels)
			metrics.RecordLatency(collector, 10.0, now.Add(time.Duration(i)*time.Millisecond), svcLabels)
		}
		exec.mu.Lock()
		exec.resourceManagers[runID] = rm
		exec.mu.Unlock()
		opt := &simulationv1.OptimizationConfig{
			Online:                    true,
			TargetP95LatencyMs:        50.0,
			ControlIntervalMs:         15,
			OptimizationTargetPrimary: "cpu_utilization",
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm)
		time.Sleep(100 * time.Millisecond)
		cancel()
		time.Sleep(30 * time.Millisecond)
		rec, ok := store.Get(runID)
		if !ok {
			t.Fatalf("run not found")
		}
		if rec.Run.Iterations < 1 {
			t.Errorf("expected at least one optimization progress (iteration >= 1), got %d", rec.Run.Iterations)
		}
		if rec.Run.BestScore < 0 || rec.Run.BestScore > 1 {
			t.Errorf("expected best_score in [0,1] for cpu_utilization primary, got %f", rec.Run.BestScore)
		}
	})
}

// Test that the online controller prefers vertical scaling (CPU cores per instance)
// when latency is above target and service CPU utilization is high.
func TestRunExecutorOnlineControllerPrefersVerticalScaleUpOnHighCPU(t *testing.T) {
	exec := NewRunExecutor(NewRunStore(), nil)
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

// Test that the online controller scales out hosts up to max_hosts when hosts are hot
// and latency is above target, before resorting to vertical host capacity increases.
func TestRunExecutorOnlineControllerScalesOutHostsBeforeVertical(t *testing.T) {
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "online-host-scale-out"

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 4}},
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

	// High latency to trigger scaling.
	now := time.Now()
	svcLabels := metrics.CreateServiceLabels("svc1")
	for i := 0; i < 5; i++ {
		ts := now.Add(time.Duration(i) * time.Millisecond)
		metrics.RecordLatency(collector, 200.0, ts, svcLabels)
	}

	// Mark the existing host as hot so host-level scaling is considered.
	if host, ok := rm.GetHost("host-1"); ok {
		host.SetCPUUtilization(0.9)
	}

	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		TargetP95LatencyMs: 50.0,
		ControlIntervalMs:  10,
		StepSize:           1.0,
		MinHosts:           1,
		MaxHosts:           3,
	}

	initialHosts := rm.HostCount()
	if initialHosts != 1 {
		t.Fatalf("expected initial HostCount 1, got %d", initialHosts)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm)

	time.Sleep(100 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	hostCount := rm.HostCount()
	if hostCount <= initialHosts {
		t.Fatalf("expected host count to scale out above %d, got %d", initialHosts, hostCount)
	}
	if hostCount > int(opt.MaxHosts) {
		t.Fatalf("expected host count to not exceed max_hosts=%d, got %d", opt.MaxHosts, hostCount)
	}
}

// Test that when P95 and host CPU are low and scale_down_host_cpu_util_max is set,
// the online controller scales in empty hosts (Phase 3).
func TestRunExecutorOnlineControllerScaleInHosts(t *testing.T) {
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "online-host-scale-in"

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 4}},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 1,
				Model:    "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/test", MeanCPUMs: 10, CPUSigmaMs: 2, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.5}},
				},
			},
		},
	}

	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	if err := rm.ScaleOutHosts(2); err != nil {
		t.Fatalf("ScaleOutHosts(2): %v", err)
	}
	if rm.HostCount() != 2 {
		t.Fatalf("expected 2 hosts after scale-out, got %d", rm.HostCount())
	}

	collector := metrics.NewCollector()
	collector.Start()
	svcLabels := metrics.CreateServiceLabels("svc1")
	now := time.Now()
	for i := 0; i < 10; i++ {
		ts := now.Add(time.Duration(i) * time.Millisecond)
		metrics.RecordLatency(collector, 5.0, ts, svcLabels)
		metrics.RecordCPUUtilization(collector, 0.2, ts, svcLabels)
	}
	for _, hid := range rm.HostIDs() {
		if h, ok := rm.GetHost(hid); ok {
			h.SetCPUUtilization(0.2)
		}
	}

	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:                  true,
		TargetP95LatencyMs:      100.0,
		ControlIntervalMs:       10,
		StepSize:                1.0,
		MinHosts:                1,
		MaxHosts:                3,
		ScaleDownHostCpuUtilMax: 0.5,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm)

	time.Sleep(100 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	hostCount := rm.HostCount()
	if hostCount >= 2 {
		t.Errorf("expected host scale-in when P95 and host CPU low, got host_count=%d", hostCount)
	}
	if hostCount < 1 {
		t.Errorf("expected at least 1 host (min_hosts), got %d", hostCount)
	}
}

// Test that when already at max_hosts and hosts are hot, the controller increases
// host CPU capacity instead of adding more hosts.
func TestRunExecutorOnlineControllerIncreasesHostCapacityAtMaxHosts(t *testing.T) {
	exec := NewRunExecutor(NewRunStore(), nil)
	runID := "online-host-vertical-scale"

	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host-1", Cores: 2},
			{ID: "host-2", Cores: 2},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
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

	now := time.Now()
	svcLabels := metrics.CreateServiceLabels("svc1")
	for i := 0; i < 5; i++ {
		ts := now.Add(time.Duration(i) * time.Millisecond)
		metrics.RecordLatency(collector, 200.0, ts, svcLabels)
	}

	// Mark hosts as hot.
	for _, hostID := range rm.HostIDs() {
		if host, ok := rm.GetHost(hostID); ok {
			host.SetCPUUtilization(0.9)
		}
	}

	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:             true,
		TargetP95LatencyMs: 50.0,
		ControlIntervalMs:  10,
		StepSize:           1.0,
		MinHosts:           2,
		MaxHosts:           2,
	}

	initialHostCount := rm.HostCount()
	if initialHostCount != 2 {
		t.Fatalf("expected initial HostCount 2, got %d", initialHostCount)
	}
	// Capture initial cores for one host to verify capacity increases.
	host, ok := rm.GetHost("host-1")
	if !ok {
		t.Fatalf("expected host-1 to exist")
	}
	initialCores := host.CPUCores()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go exec.runOnlineController(ctx, runID, scenario, collector, opt, rm)

	time.Sleep(100 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	if got := rm.HostCount(); got != initialHostCount {
		t.Fatalf("expected HostCount to remain %d at max_hosts, got %d", initialHostCount, got)
	}
	updatedHost, ok := rm.GetHost("host-1")
	if !ok {
		t.Fatalf("expected host-1 to still exist")
	}
	if updatedHost.CPUCores() <= initialCores {
		t.Fatalf("expected host CPU cores to increase above %d, got %d", initialCores, updatedHost.CPUCores())
	}
}

func TestRunExecutorCallbackWithInvalidURL(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
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
	exec := NewRunExecutor(NewRunStore(), nil)
	_, err := exec.Start("nope")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestRunExecutorStartOnEmptyRunID(t *testing.T) {
	exec := NewRunExecutor(NewRunStore(), nil)
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

	exec := NewRunExecutor(store, nil)
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

	exec := NewRunExecutor(store, nil)
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
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_STOPPED {
		t.Fatalf("expected stopped, got %v", rec.Run.Status)
	}
}

func TestRunExecutorStopOnEmptyRunID(t *testing.T) {
	exec := NewRunExecutor(NewRunStore(), nil)
	_, err := exec.Stop("")
	if err == nil {
		t.Fatalf("expected error")
	}
	if err != ErrRunIDMissing {
		t.Fatalf("expected ErrRunIDMissing, got %v", err)
	}
}

func TestRunExecutorStopOnNonExistentRun(t *testing.T) {
	exec := NewRunExecutor(NewRunStore(), nil)
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

	exec := NewRunExecutor(store, nil)
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

	exec := NewRunExecutor(store, nil)
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

	exec := NewRunExecutor(store, nil)
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

	exec := NewRunExecutor(store, nil)
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
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_STOPPED {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatalf("expected run to exist")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_STOPPED {
		t.Fatalf("expected stopped, got %v", rec.Run.Status)
	}
}
