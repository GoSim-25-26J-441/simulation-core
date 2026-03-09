package simd

import (
	"strings"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

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
