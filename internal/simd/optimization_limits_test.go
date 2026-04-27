package simd

import (
	"strings"
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestValidateOptimizationPreStartRejectsUnsafeConfig(t *testing.T) {
	lim := defaultOptimizationSafetyLimits()
	lim.MaxEvaluations = 10
	lim.MaxIterations = 5
	lim.AllowBatch = false

	input := &simulationv1.RunInput{
		Optimization: &simulationv1.OptimizationConfig{
			MaxEvaluations: 11,
		},
	}
	if err := validateOptimizationPreStart(input, lim); err == nil || !strings.Contains(err.Error(), "max_evaluations") {
		t.Fatalf("expected max_evaluations rejection, got %v", err)
	}

	input.Optimization.MaxEvaluations = 5
	input.Optimization.MaxIterations = 6
	if err := validateOptimizationPreStart(input, lim); err == nil || !strings.Contains(err.Error(), "max_iterations") {
		t.Fatalf("expected max_iterations rejection, got %v", err)
	}

	input.Optimization.MaxIterations = 3
	input.Optimization.Batch = &simulationv1.BatchOptimizationConfig{BeamWidth: 2}
	if err := validateOptimizationPreStart(input, lim); err == nil || !strings.Contains(err.Error(), "batch optimization disabled") {
		t.Fatalf("expected batch disable rejection, got %v", err)
	}
}

func TestValidateOptimizationPreStartRejectsUnknownObjective(t *testing.T) {
	lim := defaultOptimizationSafetyLimits()
	input := &simulationv1.RunInput{
		Optimization: &simulationv1.OptimizationConfig{
			Objective: "unknown_metric",
		},
	}
	if err := validateOptimizationPreStart(input, lim); err == nil || !strings.Contains(err.Error(), "unsupported optimization objective") {
		t.Fatalf("expected unsupported objective rejection, got %v", err)
	}
}

