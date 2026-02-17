package simd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

type mockOptimizationRunner struct{}

func (m *mockOptimizationRunner) RunExperiment(ctx context.Context, runID string, scenario *config.Scenario, durationMs int64, params *OptimizationParams) (string, float64, int32, error) {
	return "best", 0.0, 0, nil
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
		DurationMs:   1000,
		Optimization: &simulationv1.OptimizationConfig{
			Objective:     "p95_latency_ms",
			MaxIterations: 3,
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
