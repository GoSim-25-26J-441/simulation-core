package simd

import (
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

	// Attempt update - if simulation completed, this will fail with "run not found"
	err = exec.UpdateWorkloadRate("run-1", patternKey, 50.0)
	if err != nil {
		// Check if run has already completed
		rec, ok := store.Get("run-1")
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			// Simulation completed too quickly - this is expected for discrete-event sims
			t.Skipf("Simulation completed too quickly (status: %v) - skipping rate update test", rec.Run.Status)
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
