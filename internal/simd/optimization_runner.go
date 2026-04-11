package simd

import (
	"context"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// OptimizationParams configures an optimization run (objective, iterations, step size).
type OptimizationParams struct {
	Objective      string  // e.g. "p95_latency_ms", "throughput_rps"
	MaxIterations  int32   // default 10 (number of improvement steps; each step may evaluate many configs)
	StepSize       float64 // default 1.0
	MaxEvaluations int32   // optional cap on total simulation runs (0 = no cap)
	TargetUtilLow  float64 // for cpu_utilization/memory_utilization: desired band low (0 = not set)
	TargetUtilHigh float64 // for cpu_utilization/memory_utilization: desired band high
	// When non-nil and online mode is off, batch beam-search optimization is used.
	Batch *simulationv1.BatchOptimizationConfig
}

// OptimizationRunner runs a multi-run optimization experiment.
// Implementations (e.g. improvement.Orchestrator) are injected at startup to avoid circular imports.
type OptimizationRunner interface {
	// RunExperiment runs an optimization experiment and returns the best run ID, score, iteration count,
	// and the list of candidate run IDs (evaluation runs from the optimizer). runID is the optimization
	// run ID (for progress reporting to SSE subscribers).
	RunExperiment(ctx context.Context, runID string, scenario *config.Scenario, durationMs int64, params *OptimizationParams) (bestRunID string, bestScore float64, iterations int32, candidateRunIDs []string, err error)
}
