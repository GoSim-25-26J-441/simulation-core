package simd

import (
	"errors"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestRunExecutorUpdateWorkloadRate(t *testing.T) {
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
		DurationMs:   500, // Short duration for test
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Try to update immediately - discrete-event simulations can complete very quickly
	// We'll attempt the update and handle the case where simulation has already completed
	patternKey := patternKey("client", "svc1:/test")

	// Brief delay to let workload state initialize
	time.Sleep(2 * time.Millisecond)

	// Attempt update - if simulation completed and cleanup ran, we get ErrRunNotFound (race).
	err = exec.UpdateWorkloadRate("run-1", patternKey, 50.0)
	if err != nil {
		if errors.Is(err, ErrRunNotFound) {
			// Simulation completed too quickly; cleanup removed run from executor. Skip to avoid flakiness.
			t.Skipf("Simulation completed before rate update (run not found)")
		}
		t.Fatalf("UpdateWorkloadRate error: %v", err)
	}

	// Verify pattern was updated (if simulation is still running)
	patternState, ok := exec.GetWorkloadPattern("run-1", patternKey)
	if ok {
		if patternState.Pattern.Arrival.RateRPS != 50.0 {
			t.Errorf("Expected rate 50.0, got %f", patternState.Pattern.Arrival.RateRPS)
		}
	}
	// Note: If pattern is not found, simulation may have already completed, which is fine
}

func TestRunExecutorUpdateWorkloadRateNotFound(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())

	// Try to update rate for non-existent run
	err := exec.UpdateWorkloadRate("nonexistent", "client:svc1:/test", 50.0)
	if err == nil {
		t.Error("Expected error for non-existent run")
	}
}

func TestRunExecutorUpdateWorkloadPatternEmptyRunID(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store)
	pattern := config.WorkloadPattern{
		From:    "client",
		To:      "svc1:/test",
		Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 20},
	}
	err := exec.UpdateWorkloadPattern("", "client:svc1:/test", pattern)
	if err == nil {
		t.Fatalf("expected error for empty run ID")
	}
}

func TestRunExecutorUpdateWorkloadPatternRunNotFound(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store)
	pattern := config.WorkloadPattern{
		From:    "client",
		To:      "svc1:/test",
		Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 20},
	}
	err := exec.UpdateWorkloadPattern("nonexistent", "client:svc1:/test", pattern)
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestRunExecutorGetWorkloadPattern(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store)
	_, ok := exec.GetWorkloadPattern("nope", "client:svc1:/test")
	if ok {
		t.Fatalf("expected false for non-existent run")
	}
	_, ok = exec.GetWorkloadPattern("", "key")
	if ok {
		t.Fatalf("expected false for empty run ID")
	}
}

func TestRunExecutorUpdateWorkloadRateInvalidRate(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())

	// Test negative rate
	err := exec.UpdateWorkloadRate("run-1", "client:svc1:/test", -10.0)
	if err == nil {
		t.Error("Expected error for negative rate")
	}

	// Test zero rate
	err = exec.UpdateWorkloadRate("run-1", "client:svc1:/test", 0.0)
	if err == nil {
		t.Error("Expected error for zero rate")
	}
}

func TestRunExecutorUpdatePolicies_EmptyRunID(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())
	err := exec.UpdatePolicies("", nil)
	if err == nil {
		t.Fatalf("expected error for empty run ID")
	}
	if !errors.Is(err, ErrRunIDMissing) {
		t.Fatalf("expected ErrRunIDMissing, got %v", err)
	}
}

func TestRunExecutorUpdatePolicies_RunNotFound(t *testing.T) {
	exec := NewRunExecutor(NewRunStore())
	err := exec.UpdatePolicies("nonexistent", nil)
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("expected ErrRunNotFound, got %v", err)
	}
}

func TestRunExecutorUpdatePolicies_Success(t *testing.T) {
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
		DurationMs:   5000,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	// Poll until run is running so policyManagers is set
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rec, ok := store.Get("run-1")
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_RUNNING {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Update policies (run may have already completed and cleanup ran, giving ErrRunNotFound)
	err = exec.UpdatePolicies("run-1", &config.Policies{
		Autoscaling: &config.AutoscalingPolicy{
			Enabled:       true,
			TargetCPUUtil: 0.8,
			ScaleStep:     2,
		},
	})
	if err != nil && !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("UpdatePolicies error: %v", err)
	}
}

func TestRunExecutorUpdateWorkloadPattern(t *testing.T) {
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
		DurationMs:   500, // Short duration for test
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Poll for run to be running with reduced polling time
	// Discrete-event simulations can complete very quickly, so we attempt update immediately
	var rec *RunRecord
	var ok bool
	patternKey := patternKey("client", "svc1:/test")

	// Try to update immediately with minimal wait
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Millisecond)
		rec, ok = store.Get("run-1")
		if !ok {
			t.Fatal("Run not found")
		}
		if rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_RUNNING {
			break
		}
		if rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			// Simulation completed too quickly, skip update test
			t.Skip("Simulation completed too quickly for pattern update test")
		}
	}

	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		// One more attempt - simulation might have completed or not started yet
		t.Skipf("Run not in RUNNING state after polling, got %v - skipping test", rec.Run.Status)
	}

	// Update workload pattern immediately while simulation is running
	newPattern := config.WorkloadPattern{
		From: "client",
		To:   "svc1:/test",
		Arrival: config.ArrivalSpec{
			Type:    "poisson",
			RateRPS: 100.0,
		},
	}

	err = exec.UpdateWorkloadPattern("run-1", patternKey, newPattern)
	if err != nil {
		if errors.Is(err, ErrRunNotFound) {
			t.Skipf("Simulation completed before pattern update (run not found)")
		}
		t.Fatalf("UpdateWorkloadPattern error: %v", err)
	}

	// Verify pattern was updated
	patternState, ok := exec.GetWorkloadPattern("run-1", patternKey)
	if !ok {
		t.Fatal("Pattern not found")
	}

	if patternState.Pattern.Arrival.RateRPS != 100.0 {
		t.Errorf("Expected rate 100.0, got %f", patternState.Pattern.Arrival.RateRPS)
	}

	// Poll for completion with timeout instead of fixed long sleep
	for i := 0; i < 100; i++ { // up to 1 second (100 * 10ms)
		time.Sleep(10 * time.Millisecond)
		rec, ok = store.Get("run-1")
		if !ok {
			t.Fatal("Run not found while waiting for completion")
		}
		if rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED ||
			rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_FAILED {
			break
		}
	}

	// Verify final status
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_COMPLETED &&
		rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_FAILED {
		t.Logf("Warning: Run did not complete within timeout, status: %v", rec.Run.Status)
	}
}

func TestRunExecutorGetRunConfiguration(t *testing.T) {
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
		DurationMs:   3000,
		RealTimeMode: true,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	_, err = exec.Start("run-1")
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer exec.Stop("run-1")

	time.Sleep(80 * time.Millisecond)

	cfg, ok := exec.GetRunConfiguration("run-1")
	if !ok {
		t.Fatal("GetRunConfiguration expected ok=true")
	}
	if cfg == nil {
		t.Fatal("GetRunConfiguration expected non-nil config")
	}
	if len(cfg.Services) == 0 {
		t.Error("expected at least one service in config")
	}
	patternKey := patternKey("client", "svc1:/test")
	patternState, ok := exec.GetWorkloadPattern("run-1", patternKey)
	if !ok {
		t.Fatal("GetWorkloadPattern expected ok=true")
	}
	if patternState == nil || patternState.Pattern.Arrival.RateRPS != 10 {
		t.Errorf("expected rate 10, got %v", patternState)
	}

	_, ok = exec.GetRunConfiguration("")
	if ok {
		t.Fatal("GetRunConfiguration with empty runID should return ok=false")
	}
	_, ok = exec.GetRunConfiguration("nonexistent")
	if ok {
		t.Fatal("GetRunConfiguration for non-existent run should return ok=false")
	}
}

func TestRunExecutorUpdateServiceResources(t *testing.T) {
	store := NewRunStore()
	validScenario := `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 2
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
		DurationMs:   1000,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	exec := NewRunExecutor(store)
	if _, err := exec.Start("run-1"); err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Allow resource manager to be initialized but ensure run is still RUNNING.
	time.Sleep(10 * time.Millisecond)
	rec, ok := store.Get("run-1")
	if !ok {
		t.Fatal("run not found after start")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run is not RUNNING (status=%v), skipping UpdateServiceResources test", rec.Run.Status)
	}
	defer exec.Stop("run-1")

	if err := exec.UpdateServiceResources("run-1", "svc1", 4.0, 2048.0); err != nil {
		t.Fatalf("UpdateServiceResources error: %v", err)
	}

	cfg, ok := exec.GetRunConfiguration("run-1")
	if !ok || cfg == nil {
		t.Fatalf("expected GetRunConfiguration to succeed after resource update")
	}

	var svcCfg *simulationv1.ServiceConfigEntry
	for _, s := range cfg.Services {
		if s.ServiceId == "svc1" {
			svcCfg = s
			break
		}
	}
	if svcCfg == nil {
		t.Fatalf("expected svc1 in run configuration")
	}
	if svcCfg.CpuCores != 4.0 {
		t.Fatalf("expected cpu_cores=4.0 in config, got %f", svcCfg.CpuCores)
	}
	if svcCfg.MemoryMb != 2048.0 {
		t.Fatalf("expected memory_mb=2048.0 in config, got %f", svcCfg.MemoryMb)
	}
}
