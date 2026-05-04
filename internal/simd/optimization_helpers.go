package simd

import (
	"strings"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

// Optimization history / replay (RunRecord.OptimizationStep, GET /v1/runs/{id}, export):
// UIs should treat objective_score + objective_unit as the selected online objective when
// primary_target is set (cpu_utilization, memory_utilization, p95_latency). For CPU- or
// memory-primary runs, current_p95_ms and guardrail_p95_ms are latency guardrails; legacy
// target_p95_ms / score_p95_ms remain populated for backwards compatibility and must not be
// interpreted as the primary objective when primary_target is not p95_latency.
// Memory-primary objective_score may read as 0 when service memory utilization is not modeled.
//
// Run metrics: throughput_rps is cumulative aggregate work across hops; for live ingress
// response use ingress_requests deltas or ingress_throughput_rps when tuning online workloads.

// ObjectiveAndUnitForProgress returns the display objective name and unit for
// optimization_progress events. For online runs it uses optimization_target_primary;
// for batch runs it uses optimization.objective so that cpu_utilization and
// memory_utilization show the correct objective and unit (ratio).
func ObjectiveAndUnitForProgress(opt *simulationv1.OptimizationConfig) (objective string, unit string) {
	if opt == nil {
		return "p95_latency", "ms"
	}
	if opt.GetOnline() {
		objective = strings.TrimSpace(strings.ToLower(opt.GetOptimizationTargetPrimary()))
		if objective == "" {
			objective = "p95_latency"
		}
		unit = "ms"
		if objective == "cpu_utilization" || objective == "memory_utilization" {
			unit = "ratio"
		}
		return objective, unit
	}
	// Batch: use optimization.objective; normalize to display form for SSE/docs parity
	raw := strings.TrimSpace(strings.ToLower(opt.GetObjective()))
	if raw == "" {
		raw = "p95_latency_ms"
	}
	switch raw {
	case "p95_latency_ms":
		objective = "p95_latency"
		unit = "ms"
	case "p99_latency_ms":
		objective = "p99_latency"
		unit = "ms"
	case "mean_latency_ms":
		objective = "mean_latency"
		unit = "ms"
	case "cpu_utilization", "memory_utilization":
		objective = raw
		unit = "ratio"
	case "error_rate":
		objective = raw
		unit = "ratio"
	case "throughput_rps":
		objective = raw
		unit = "rps"
	default:
		objective = raw // cost, etc.
		unit = "ms"
	}
	return objective, unit
}
