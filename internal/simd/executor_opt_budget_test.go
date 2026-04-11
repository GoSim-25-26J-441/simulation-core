package simd

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestApplyMaxEvaluationsFromOpt_BatchDefault64(t *testing.T) {
	params := &OptimizationParams{MaxEvaluations: 0}
	opt := &simulationv1.OptimizationConfig{
		Batch: &simulationv1.BatchOptimizationConfig{BeamWidth: 2},
	}
	applyMaxEvaluationsFromOpt(params, opt)
	if params.MaxEvaluations != defaultBatchMaxEvaluations {
		t.Fatalf("MaxEvaluations=%d want %d", params.MaxEvaluations, defaultBatchMaxEvaluations)
	}
}

func TestApplyMaxEvaluationsFromOpt_BatchExplicitPreserved(t *testing.T) {
	params := &OptimizationParams{MaxEvaluations: 0}
	opt := &simulationv1.OptimizationConfig{
		MaxEvaluations: 200,
		Batch:          &simulationv1.BatchOptimizationConfig{BeamWidth: 2},
	}
	applyMaxEvaluationsFromOpt(params, opt)
	if params.MaxEvaluations != 200 {
		t.Fatalf("MaxEvaluations=%d want 200", params.MaxEvaluations)
	}
}

func TestApplyMaxEvaluationsFromOpt_HillClimbZeroUnlimited(t *testing.T) {
	params := &OptimizationParams{MaxEvaluations: 0}
	opt := &simulationv1.OptimizationConfig{
		MaxIterations: 10,
	}
	applyMaxEvaluationsFromOpt(params, opt)
	if params.MaxEvaluations != 0 {
		t.Fatalf("MaxEvaluations=%d want 0 (no batch)", params.MaxEvaluations)
	}
}

func TestApplyMaxEvaluationsFromOpt_NilOpt(t *testing.T) {
	params := &OptimizationParams{MaxEvaluations: 0}
	applyMaxEvaluationsFromOpt(params, nil)
	if params.MaxEvaluations != 0 {
		t.Fatalf("MaxEvaluations=%d want 0", params.MaxEvaluations)
	}
}
