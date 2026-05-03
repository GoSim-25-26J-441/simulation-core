package improvement

import (
	"context"
	"errors"
	"fmt"
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
		TotalRequests: 1000,
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

func TestRunBatchBeamSearch_SkipsNeighborPlacementErrors(t *testing.T) {
	base := testBatchScenario()
	base.Services[0].Replicas = 4
	refineOff := false
	pb := &simulationv1.BatchOptimizationConfig{
		BeamWidth:                 4,
		MaxSearchDepth:            1,
		MaxNeighborsPerState:      24,
		ReevaluationsPerCandidate: 1,
		EnableLocalRefinement:     &refineOff,
	}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatalf("ParseBatchSpec: %v", err)
	}
	eval := func(sc *config.Scenario) (*simulationv1.RunMetrics, int, error) {
		if sc.Services[0].Replicas > base.Services[0].Replicas {
			return nil, 0, fmt.Errorf("resource initialization failed: cannot place service svc1: insufficient host capacity (each instance needs 1.00 CPU cores, 512.00 MB memory)")
		}
		return feasibleBatchMetrics(), 1, nil
	}
	res, err := RunBatchBeamSearch(context.Background(), spec, base, 0, NewCandidateStore(), eval)
	if err != nil {
		t.Fatalf("RunBatchBeamSearch: %v", err)
	}
	if res.FailedCandidateEvaluations < 1 {
		t.Fatalf("FailedCandidateEvaluations=%d want >= 1", res.FailedCandidateEvaluations)
	}
	if res.BestScenario != nil && res.BestScenario.Services[0].Replicas > base.Services[0].Replicas {
		t.Fatalf("best scenario should not be a failing scale-out neighbor")
	}
}

func TestBatchSearchDiagnosticsProtoMapsCounts(t *testing.T) {
	p := BatchSearchDiagnosticsProto(&BatchSearchResult{
		GeneratedNeighbors:         100,
		RejectedStaticCapacity:     5,
		RejectedBounds:             4,
		RejectedPlacement:          9,
		EvaluatedCandidates:        42,
		FailedCandidateEvaluations: 3,
	})
	if p == nil || p.GetGeneratedNeighbors() != 100 || p.GetRejectedPlacement() != 9 || p.GetEvaluatedCandidates() != 42 {
		t.Fatalf("unexpected proto: %+v", p)
	}
	if BatchSearchDiagnosticsProto(nil) != nil {
		t.Fatal("nil input should yield nil proto")
	}
}

func TestRunBatchBeamSearch_RealWorldBatchPayloadShapeCompletes(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 32, MemoryGB: 64},
			{ID: "h2", Cores: 32, MemoryGB: 64},
		},
		Services: []config.Service{
			{ID: "svc1", Replicas: 2, CPUCores: 1, MemoryMB: 512, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, NetLatencyMs: config.LatencySpec{Mean: 1}}}},
		},
	}
	actions := make([]simulationv1.BatchScalingAction, 0, 12)
	for a := simulationv1.BatchScalingAction_SERVICE_SCALE_OUT; a <= simulationv1.BatchScalingAction_HOST_SCALE_DOWN_MEMORY; a++ {
		actions = append(actions, a)
	}
	refineOff := false
	pb := &simulationv1.BatchOptimizationConfig{
		AllowedActions:            actions,
		MaxNeighborsPerState:      24,
		MaxSearchDepth:            5,
		BeamWidth:                 8,
		ReevaluationsPerCandidate: 3,
		EnableLocalRefinement:     &refineOff,
	}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatalf("ParseBatchSpec: %v", err)
	}
	eval := func(*config.Scenario) (*simulationv1.RunMetrics, int, error) {
		return feasibleBatchMetrics(), 3, nil
	}
	res, err := RunBatchBeamSearch(context.Background(), spec, base, 128, NewCandidateStore(), eval)
	if err != nil {
		t.Fatalf("RunBatchBeamSearch: %v", err)
	}
	if res.GeneratedNeighbors == 0 && res.EvaluatedCandidates == 0 {
		t.Fatalf("expected diagnostics populated, got %+v", res)
	}
}

func TestRunBatchBeamSearch_BaselinePlacementErrorIsFatal(t *testing.T) {
	base := testBatchScenario()
	refineOff := false
	pb := &simulationv1.BatchOptimizationConfig{
		EnableLocalRefinement: &refineOff,
		MaxSearchDepth:        1,
		MaxNeighborsPerState:  4,
	}
	spec, err := batchspec.ParseBatchSpec(pb, base)
	if err != nil {
		t.Fatalf("ParseBatchSpec: %v", err)
	}
	eval := func(*config.Scenario) (*simulationv1.RunMetrics, int, error) {
		return nil, 0, fmt.Errorf("resource initialization failed: cannot place service svc1")
	}
	_, err = RunBatchBeamSearch(context.Background(), spec, base, 0, NewCandidateStore(), eval)
	if err == nil {
		t.Fatal("expected fatal baseline eval error")
	}
}
