package simd

import (
	"context"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// OptimizationParams configures an optimization run (objective, iterations, step size).
type OptimizationParams struct {
	Objective     string  // e.g. "p95_latency_ms", "throughput_rps"
	MaxIterations int32   // default 10
	StepSize      float64 // default 1.0
}

// OptimizationRunner runs a multi-run optimization experiment.
// Implementations (e.g. improvement.Orchestrator) are injected at startup to avoid circular imports.
type OptimizationRunner interface {
	// RunExperiment runs an optimization experiment and returns the best run ID, score, and iteration count.
	RunExperiment(ctx context.Context, scenario *config.Scenario, durationMs int64, params *OptimizationParams) (bestRunID string, bestScore float64, iterations int32, err error)
}
