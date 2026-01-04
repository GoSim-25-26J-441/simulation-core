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
