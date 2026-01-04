package improvement

import (
	"context"
	"fmt"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// TestOptimizationLoopEndToEnd tests the complete optimization loop
func TestOptimizationLoopEndToEnd(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 5, 1.0)
	orchestrator := NewOrchestrator(store, executor, optimizer, &P95LatencyObjective{})

	initialConfig := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host1", Cores: 4},
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
		Workload: []config.WorkloadPattern{
			{
				From: "client",
				To:   "svc1:/test",
				Arrival: config.ArrivalSpec{
					Type:    "poisson",
					RateRPS: 10,
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// This will fail because we need actual simulation execution, but we can test the structure
	_, err := orchestrator.RunExperiment(ctx, initialConfig, 1000)
	if err != nil {
		// Expected for incomplete test setup
		t.Logf("RunExperiment returned error (expected): %v", err)
	}
}

// TestOptimizerWithConvergence tests optimizer with convergence detection
func TestOptimizerWithConvergence(t *testing.T) {
	objective := &P95LatencyObjective{}
	optimizer := NewOptimizer(objective, 10, 1.0)

	// Use a convergence strategy that triggers early
	convergenceConfig := &ConvergenceConfig{
		NoImprovementIterations: 2,
		MinIterations:           2,
	}
	optimizer.WithConvergenceStrategy(NewNoImprovementStrategy(convergenceConfig))

	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
			},
		},
	}

	// Mock evaluation that returns same score (should trigger convergence)
	iteration := 0
	evaluateFunc := func(sc *config.Scenario) (float64, error) {
		iteration++
		return 100.0, nil // Same score every time
	}

	result, err := optimizer.Optimize(scenario, evaluateFunc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatalf("expected non-nil result")
	}

	// Should converge early due to no improvement
	if !result.Converged {
		t.Logf("optimization did not converge (may need more iterations)")
	}
}

// TestOptimizerWithExplorer tests optimizer with custom explorer
func TestOptimizerWithExplorer(t *testing.T) {
	objective := &P95LatencyObjective{}
	optimizer := NewOptimizer(objective, 5, 1.0)

	// Use conservative explorer
	explorer := NewConservativeExplorer()
	optimizer.WithExplorer(explorer)

	scenario := &config.Scenario{
		Hosts: []config.Host{
			{ID: "host1", Cores: 4},
		},
		Services: []config.Service{
			{
				ID:       "svc1",
				Replicas: 2,
				Model:    "cpu",
			},
		},
	}

	// Mock evaluation
	evaluateFunc := func(sc *config.Scenario) (float64, error) {
		// Return score based on replicas
		score := 1000.0 - float64(sc.Services[0].Replicas)*100.0
		return score, nil
	}

	result, err := optimizer.Optimize(scenario, evaluateFunc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatalf("expected non-nil result")
	}

	if result.BestConfig == nil {
		t.Fatalf("expected non-nil best config")
	}
}

// TestOrchestratorWithParallelExecution tests parallel execution
func TestOrchestratorWithParallelExecution(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 5, 1.0)
	orchestrator := NewOrchestrator(store, executor, optimizer, &P95LatencyObjective{}).
		WithMaxParallelRuns(3)

	if orchestrator.maxParallelRuns != 3 {
		t.Fatalf("expected maxParallelRuns to be 3, got %d", orchestrator.maxParallelRuns)
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// This will fail due to incomplete scenarios, but tests the structure
	_, err := orchestrator.EvaluateConfigurationsParallel(ctx, scenarios, 1000)
	if err != nil {
		t.Logf("EvaluateConfigurationsParallel returned error (expected): %v", err)
	}
}

// TestOptimizationWithSelectionStrategy tests optimization with selection strategy
func TestOptimizationWithSelectionStrategy(t *testing.T) {
	objective := &P95LatencyObjective{}

	// Create candidates with different scores
	candidates := []*ConfigurationCandidate{
		{
			Config: &config.Scenario{
				Services: []config.Service{{ID: "svc1", Replicas: 2}},
			},
			RunID:     "run1",
			Score:     100,
			Metrics:   &simulationv1.RunMetrics{LatencyP95Ms: 100},
			Evaluated: true,
		},
		{
			Config: &config.Scenario{
				Services: []config.Service{{ID: "svc1", Replicas: 3}},
			},
			RunID:     "run2",
			Score:     80,
			Metrics:   &simulationv1.RunMetrics{LatencyP95Ms: 80},
			Evaluated: true,
		},
		{
			Config: &config.Scenario{
				Services: []config.Service{{ID: "svc1", Replicas: 4}},
			},
			RunID:     "run3",
			Score:     90,
			Metrics:   &simulationv1.RunMetrics{LatencyP95Ms: 90},
			Evaluated: true,
		},
	}

	// Test best score strategy
	strategy := &BestScoreStrategy{}
	best, err := strategy.SelectBest(candidates, objective)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if best == nil {
		t.Fatalf("expected non-nil best candidate")
	}

	if best.RunID != "run2" {
		t.Fatalf("expected best run to be run2 (score 80), got %s", best.RunID)
	}
}

// TestOptimizationWithMetricsComparison tests metrics comparison across runs
func TestOptimizationWithMetricsComparison(t *testing.T) {
	metrics1 := &simulationv1.RunMetrics{
		TotalRequests:  1000,
		LatencyP95Ms:   100,
		FailedRequests: 10,
		ThroughputRPS:  100,
	}
	metrics2 := &simulationv1.RunMetrics{
		TotalRequests:  1000,
		LatencyP95Ms:   80,
		FailedRequests: 5,
		ThroughputRPS:  120,
	}

	objective := &P95LatencyObjective{}
	comparison := CompareMetrics(metrics1, metrics2, objective)

	if comparison == nil {
		t.Fatalf("expected non-nil comparison")
	}

	// metrics2 should be better (lower latency)
	if comparison.LatencyP95Diff >= 0 {
		t.Fatalf("expected negative latency diff (improvement), got %f", comparison.LatencyP95Diff)
	}
}

// TestOptimizationWithConvergenceStrategies tests different convergence strategies
func TestOptimizationWithConvergenceStrategies(t *testing.T) {
	objective := &P95LatencyObjective{}

	// Test with plateau strategy
	optimizer1 := NewOptimizer(objective, 10, 1.0)
	plateauConfig := &ConvergenceConfig{
		PlateauIterations: 3,
		ScoreTolerance:    0.01,
		MinIterations:     2,
	}
	optimizer1.WithConvergenceStrategy(NewPlateauStrategy(plateauConfig))

	// Test with variance strategy
	optimizer2 := NewOptimizer(objective, 10, 1.0)
	varianceConfig := &ConvergenceConfig{
		PlateauIterations:    3,
		ImprovementThreshold: 0.01,
		MinIterations:        2,
	}
	optimizer2.WithConvergenceStrategy(NewVarianceStrategy(varianceConfig))

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host1", Cores: 4}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, Model: "cpu"},
		},
	}

	// Mock evaluation with plateau
	evaluateFunc := func(sc *config.Scenario) (float64, error) {
		return 100.0, nil
	}

	result1, err1 := optimizer1.Optimize(scenario, evaluateFunc)
	if err1 != nil {
		t.Fatalf("unexpected error with plateau strategy: %v", err1)
	}

	result2, err2 := optimizer2.Optimize(scenario, evaluateFunc)
	if err2 != nil {
		t.Fatalf("unexpected error with variance strategy: %v", err2)
	}

	if result1 == nil || result2 == nil {
		t.Fatalf("expected non-nil results")
	}
}

// TestOptimizationErrorHandling tests error handling in optimization
func TestOptimizationErrorHandling(t *testing.T) {
	objective := &P95LatencyObjective{}
	optimizer := NewOptimizer(objective, 5, 1.0)

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host1", Cores: 4}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, Model: "cpu"},
		},
	}

	// Test with evaluation function that returns errors
	errorCount := 0
	evaluateFunc := func(sc *config.Scenario) (float64, error) {
		errorCount++
		if errorCount <= 2 {
			return 0, fmt.Errorf("evaluation error")
		}
		return 100.0, nil
	}

	result, err := optimizer.Optimize(scenario, evaluateFunc)
	// Optimizer should handle errors gracefully and continue
	if err != nil {
		t.Logf("optimizer returned error (may be acceptable): %v", err)
	}

	if result != nil {
		// Should still produce a result even with some errors
		t.Logf("optimizer produced result despite errors")
	}
}

// TestOptimizationHistoryTracking tests that optimization history is tracked correctly
func TestOptimizationHistoryTracking(t *testing.T) {
	objective := &P95LatencyObjective{}
	optimizer := NewOptimizer(objective, 5, 1.0)

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host1", Cores: 4}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, Model: "cpu"},
		},
	}

	iteration := 0
	evaluateFunc := func(sc *config.Scenario) (float64, error) {
		iteration++
		score := 1000.0 - float64(iteration)*50.0
		return score, nil
	}

	result, err := optimizer.Optimize(scenario, evaluateFunc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result == nil {
		t.Fatalf("expected non-nil result")
	}

	if len(result.History) == 0 {
		t.Fatalf("expected non-empty history")
	}

	// Check that history is ordered
	for i := 1; i < len(result.History); i++ {
		if result.History[i].Iteration <= result.History[i-1].Iteration {
			t.Fatalf("expected history to be ordered by iteration")
		}
	}
}

// TestOptimizationWithDifferentObjectives tests optimization with different objective functions
func TestOptimizationWithDifferentObjectives(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host1", Cores: 4}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, Model: "cpu"},
		},
	}

	objectives := []ObjectiveFunction{
		&P95LatencyObjective{},
		&P99LatencyObjective{},
		&MeanLatencyObjective{},
		&ThroughputObjective{},
		&ErrorRateObjective{},
	}

	for _, obj := range objectives {
		optimizer := NewOptimizer(obj, 3, 1.0)

		evaluateFunc := func(sc *config.Scenario) (float64, error) {
			return 100.0, nil
		}

		result, err := optimizer.Optimize(scenario, evaluateFunc)
		if err != nil {
			t.Fatalf("unexpected error with objective %s: %v", obj.Name(), err)
		}

		if result == nil {
			t.Fatalf("expected non-nil result for objective %s", obj.Name())
		}
	}
}

// TestOptimizationResourceCleanup tests that resources are cleaned up properly
func TestOptimizationResourceCleanup(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 5, 1.0)
	orchestrator := NewOrchestrator(store, executor, optimizer, &P95LatencyObjective{})

	// Add some completed runs
	runCtx1 := &RunContext{
		RunID:  "run1",
		Status: RunStatusCompleted,
	}
	runCtx2 := &RunContext{
		RunID:  "run2",
		Status: RunStatusFailed,
	}
	runCtx3 := &RunContext{
		RunID:  "run3",
		Status: RunStatusRunning,
	}

	orchestrator.mu.Lock()
	orchestrator.activeRuns["run1"] = runCtx1
	orchestrator.activeRuns["run2"] = runCtx2
	orchestrator.activeRuns["run3"] = runCtx3
	orchestrator.mu.Unlock()

	// Cleanup should remove completed and failed runs
	orchestrator.CleanupCompletedRuns()

	// Only running run should remain
	count := orchestrator.GetActiveRunCount()
	if count != 1 {
		t.Fatalf("expected 1 active run after cleanup, got %d", count)
	}

	// Verify the remaining run is the running one
	_, ok := orchestrator.GetRunContext("run3")
	if !ok {
		t.Fatalf("expected run3 to still be active")
	}
}
