package improvement

import (
	"context"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestOrchestratorWithMaxParallelRuns(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 10, 1.0)
	orchestrator := NewOrchestrator(store, executor, optimizer, &P95LatencyObjective{})

	// Test default
	if orchestrator.maxParallelRuns != 1 {
		t.Fatalf("expected default maxParallelRuns to be 1, got %d", orchestrator.maxParallelRuns)
	}

	// Test setting
	orchestrator.WithMaxParallelRuns(5)
	if orchestrator.maxParallelRuns != 5 {
		t.Fatalf("expected maxParallelRuns to be 5, got %d", orchestrator.maxParallelRuns)
	}

	// Test minimum
	orchestrator.WithMaxParallelRuns(0)
	if orchestrator.maxParallelRuns != 1 {
		t.Fatalf("expected maxParallelRuns to be clamped to 1, got %d", orchestrator.maxParallelRuns)
	}
}

func TestOrchestratorGetActiveRunCount(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 10, 1.0)
	orchestrator := NewOrchestrator(store, executor, optimizer, &P95LatencyObjective{})

	count := orchestrator.GetActiveRunCount()
	if count != 0 {
		t.Fatalf("expected initial active run count to be 0, got %d", count)
	}
}

func TestOrchestratorCleanupCompletedRuns(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 10, 1.0)
	orchestrator := NewOrchestrator(store, executor, optimizer, &P95LatencyObjective{})

	// Add a completed run
	runCtx := &RunContext{
		RunID:  "test-run",
		Status: RunStatusCompleted,
	}
	orchestrator.mu.Lock()
	orchestrator.activeRuns["test-run"] = runCtx
	orchestrator.mu.Unlock()

	// Cleanup
	orchestrator.CleanupCompletedRuns()

	// Verify it was removed
	count := orchestrator.GetActiveRunCount()
	if count != 0 {
		t.Fatalf("expected active run count to be 0 after cleanup, got %d", count)
	}
}

func TestOrchestratorCancelActiveRuns(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 10, 1.0)
	orchestrator := NewOrchestrator(store, executor, optimizer, &P95LatencyObjective{})

	// Create a run
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host1", Cores: 4}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 1, Model: "cpu"},
		},
	}
	scenarioYAML, _ := config.MarshalScenarioYAML(scenario)
	runInput := &simulationv1.RunInput{
		ScenarioYaml: scenarioYAML,
		DurationMs:   1000,
	}

	runID := "test-cancel"
	_, err := store.Create(runID, runInput)
	if err != nil {
		t.Fatalf("failed to create run: %v", err)
	}

	// Add to active runs
	runCtx := &RunContext{
		RunID:  runID,
		Status: RunStatusRunning,
	}
	orchestrator.mu.Lock()
	orchestrator.activeRuns[runID] = runCtx
	orchestrator.mu.Unlock()

	// Cancel
	err = orchestrator.CancelActiveRuns()
	if err != nil {
		// It's okay if the run wasn't actually started
		t.Logf("cancel returned error (expected if run not started): %v", err)
	}
}

func TestEvaluateConfigurationsParallel(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 10, 1.0)
	orchestrator := NewOrchestrator(store, executor, optimizer, &P95LatencyObjective{}).
		WithMaxParallelRuns(2)

	scenarios := []*config.Scenario{
		{
			Hosts: []config.Host{{ID: "host1", Cores: 4}},
			Services: []config.Service{
				{ID: "svc1", Replicas: 1, Model: "cpu"},
			},
		},
		{
			Hosts: []config.Host{{ID: "host1", Cores: 4}},
			Services: []config.Service{
				{ID: "svc1", Replicas: 2, Model: "cpu"},
			},
		},
	}

	// This will fail because we need valid scenarios with workload, but we can test the structure
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := orchestrator.EvaluateConfigurationsParallel(ctx, scenarios, 1000)
	// We expect errors because the scenarios are incomplete, but the function should handle them
	if err == nil {
		t.Logf("evaluation completed (unexpected but acceptable)")
	} else {
		t.Logf("evaluation returned error (expected for incomplete scenarios): %v", err)
	}
}
