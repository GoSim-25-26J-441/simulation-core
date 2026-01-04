package improvement

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestNewOrchestrator(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 10, 1.0)
	objective := &P95LatencyObjective{}

	orch := NewOrchestrator(store, executor, optimizer, objective)
	if orch == nil {
		t.Fatalf("expected non-nil orchestrator")
	}
	if orch.store != store {
		t.Fatalf("expected store to be set")
	}
	if orch.executor != executor {
		t.Fatalf("expected executor to be set")
	}
	if orch.optimizer != optimizer {
		t.Fatalf("expected optimizer to be set")
	}
	if orch.objective != objective {
		t.Fatalf("expected objective to be set")
	}
}

func TestConfigsMatch(t *testing.T) {
	c1 := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2},
			{ID: "svc2", Replicas: 3},
		},
	}
	c2 := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2},
			{ID: "svc2", Replicas: 3},
		},
	}
	c3 := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2},
			{ID: "svc2", Replicas: 4}, // Different replica count
		},
	}
	c4 := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2},
			// Different number of services
		},
	}

	if !configsMatch(c1, c2) {
		t.Fatalf("expected c1 and c2 to match")
	}
	if configsMatch(c1, c3) {
		t.Fatalf("expected c1 and c3 not to match (different replicas)")
	}
	if configsMatch(c1, c4) {
		t.Fatalf("expected c1 and c4 not to match (different service count)")
	}

	// Test nil scenarios
	if !configsMatch(nil, nil) {
		t.Fatalf("expected nil scenarios to match")
	}
	if configsMatch(c1, nil) {
		t.Fatalf("expected c1 and nil not to match")
	}
	if configsMatch(nil, c1) {
		t.Fatalf("expected nil and c1 not to match")
	}

	// Test CPU and Memory differences
	c5 := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1.0, MemoryMB: 512},
		},
	}
	c6 := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 2.0, MemoryMB: 512}, // Different CPU
		},
	}
	c7 := &config.Scenario{
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1.0, MemoryMB: 1024}, // Different memory
		},
	}
	if configsMatch(c5, c6) {
		t.Fatalf("expected c5 and c6 not to match (different CPU)")
	}
	if configsMatch(c5, c7) {
		t.Fatalf("expected c5 and c7 not to match (different memory)")
	}

	// Test workload differences
	c8 := &config.Scenario{
		Services: []config.Service{{ID: "svc1", Replicas: 2}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 100}},
		},
	}
	c9 := &config.Scenario{
		Services: []config.Service{{ID: "svc1", Replicas: 2}},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 200}}, // Different rate
		},
	}
	if configsMatch(c8, c9) {
		t.Fatalf("expected c8 and c9 not to match (different workload rate)")
	}

	// Test policies differences
	c10 := &config.Scenario{
		Services: []config.Service{{ID: "svc1", Replicas: 2}},
		Policies: &config.Policies{
			Autoscaling: &config.AutoscalingPolicy{Enabled: true, TargetCPUUtil: 0.7, ScaleStep: 1},
		},
	}
	c11 := &config.Scenario{
		Services: []config.Service{{ID: "svc1", Replicas: 2}},
		Policies: &config.Policies{
			Autoscaling: &config.AutoscalingPolicy{Enabled: true, TargetCPUUtil: 0.8, ScaleStep: 1}, // Different target
		},
	}
	c12 := &config.Scenario{
		Services: []config.Service{{ID: "svc1", Replicas: 2}},
		// No policies
	}
	if configsMatch(c10, c11) {
		t.Fatalf("expected c10 and c11 not to match (different autoscaling policy)")
	}
	if configsMatch(c10, c12) {
		t.Fatalf("expected c10 and c12 not to match (one has policies, one doesn't)")
	}
}

func TestOrchestratorGetActiveRuns(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 10, 1.0)
	objective := &P95LatencyObjective{}

	orch := NewOrchestrator(store, executor, optimizer, objective)

	// Initially no active runs
	runs := orch.GetActiveRuns()
	if len(runs) != 0 {
		t.Fatalf("expected 0 active runs initially, got %d", len(runs))
	}
}

func TestOrchestratorGetRunContext(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	optimizer := NewOptimizer(&P95LatencyObjective{}, 10, 1.0)
	objective := &P95LatencyObjective{}

	orch := NewOrchestrator(store, executor, optimizer, objective)

	// Get non-existent run
	_, ok := orch.GetRunContext("nonexistent")
	if ok {
		t.Fatalf("expected false for non-existent run")
	}
}

// Note: Full integration tests for RunExperiment would require
// setting up a complete simulation environment with actual run execution.
// These are better suited for integration tests with build tags.
