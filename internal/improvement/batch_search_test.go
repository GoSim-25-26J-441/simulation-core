package improvement

import (
	"context"
	"errors"
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func testBatchScenario() *config.Scenario {
	return &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 16, MemoryGB: 32}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
}

func feasibleBatchMetrics() *simulationv1.RunMetrics {
	return &simulationv1.RunMetrics{
		LatencyP95Ms:  100,
		LatencyP99Ms:  200,
		ThroughputRps: 1000,
		TotalRequests:   1000,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.55, MemoryUtilization: 0.55},
		},
	}
}

func TestRunBatchBeamSearch_EvalCountIncludesReevalCost(t *testing.T) {
	base := testBatchScenario()
	refineOff := false
	pb := &simulationv1.BatchOptimizationConfig{
		BeamWidth:                 1,
		MaxSearchDepth:            1,
		MaxNeighborsPerState:      1,
		ReevaluationsPerCandidate: 4,
		EnableLocalRefinement:     &refineOff,
	}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatalf("ParseBatchSpec: %v", err)
	}
	const costPerEval = 4
	eval := func(*config.Scenario) (*simulationv1.RunMetrics, int, error) {
		return feasibleBatchMetrics(), costPerEval, nil
	}
	res, err := RunBatchBeamSearch(context.Background(), spec, base, 0, NewCandidateStore(), eval)
	if err != nil {
		t.Fatalf("RunBatchBeamSearch: %v", err)
	}
	// Baseline + one capped neighbor at depth 0.
	want := costPerEval * 2
	if res.Evaluations != want {
		t.Fatalf("Evaluations=%d want %d (baseline + 1 neighbor × reeval cost)", res.Evaluations, want)
	}
	if res.RefinementEvaluations != 0 {
		t.Fatalf("RefinementEvaluations=%d want 0 with refinement off", res.RefinementEvaluations)
	}
}

func TestRunBatchBeamSearch_BaselineExceedsBudget(t *testing.T) {
	base := testBatchScenario()
	refineOff := false
	pb := &simulationv1.BatchOptimizationConfig{
		ReevaluationsPerCandidate: 3,
		EnableLocalRefinement:     &refineOff,
		MaxNeighborsPerState:      1,
		MaxSearchDepth:            1,
	}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatalf("ParseBatchSpec: %v", err)
	}
	eval := func(*config.Scenario) (*simulationv1.RunMetrics, int, error) {
		return feasibleBatchMetrics(), 3, nil
	}
	_, err = RunBatchBeamSearch(context.Background(), spec, base, 2, NewCandidateStore(), eval)
	if err == nil || !errors.Is(err, ErrBatchBudgetExhausted) {
		t.Fatalf("expected ErrBatchBudgetExhausted, got %v", err)
	}
}

func TestRunBatchBeamSearch_StopsNeighborsWhenBudgetExhausted(t *testing.T) {
	base := testBatchScenario()
	refineOff := false
	pb := &simulationv1.BatchOptimizationConfig{
		BeamWidth:                 1,
		MaxSearchDepth:            1,
		MaxNeighborsPerState:      8,
		ReevaluationsPerCandidate: 2,
		EnableLocalRefinement:     &refineOff,
	}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatalf("ParseBatchSpec: %v", err)
	}
	eval := func(*config.Scenario) (*simulationv1.RunMetrics, int, error) {
		return feasibleBatchMetrics(), 2, nil
	}
	res, err := RunBatchBeamSearch(context.Background(), spec, base, 3, NewCandidateStore(), eval)
	if err != nil {
		t.Fatalf("RunBatchBeamSearch: %v", err)
	}
	if res.Evaluations != 2 {
		t.Fatalf("Evaluations=%d want 2 (baseline only; neighbor would exceed budget)", res.Evaluations)
	}
	if !res.BudgetExhausted {
		t.Fatalf("BudgetExhausted=false want true")
	}
	if res.EffectiveMaxEvaluations != 3 {
		t.Fatalf("EffectiveMaxEvaluations=%d want 3", res.EffectiveMaxEvaluations)
	}
}

func TestRunBatchBeamSearch_BudgetNotExhaustedWhenUnlimited(t *testing.T) {
	base := testBatchScenario()
	refineOff := false
	pb := &simulationv1.BatchOptimizationConfig{
		BeamWidth:                 1,
		MaxSearchDepth:            1,
		MaxNeighborsPerState:      1,
		ReevaluationsPerCandidate: 2,
		EnableLocalRefinement:     &refineOff,
	}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatalf("ParseBatchSpec: %v", err)
	}
	eval := func(*config.Scenario) (*simulationv1.RunMetrics, int, error) {
		return feasibleBatchMetrics(), 2, nil
	}
	res, err := RunBatchBeamSearch(context.Background(), spec, base, 0, NewCandidateStore(), eval)
	if err != nil {
		t.Fatalf("RunBatchBeamSearch: %v", err)
	}
	if res.BudgetExhausted {
		t.Fatalf("BudgetExhausted=true want false with unlimited budget")
	}
	if res.EffectiveMaxEvaluations != 0 {
		t.Fatalf("EffectiveMaxEvaluations=%d want 0", res.EffectiveMaxEvaluations)
	}
}
