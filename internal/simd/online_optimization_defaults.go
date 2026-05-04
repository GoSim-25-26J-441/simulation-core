package simd

import (
	"fmt"
	"strings"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

// Defaults for online optimization when optimization_target_primary is cpu_utilization
// or memory_utilization and target_util_* are omitted (0). Documented in proto and BACKEND_INTEGRATION.md.
const (
	DefaultOnlineTargetUtilLow  = 0.4
	DefaultOnlineTargetUtilHigh = 0.7
)

// EffectiveOnlineTargetUtilLow returns the configured low utilization bound, or the default when unset (<=0).
func EffectiveOnlineTargetUtilLow(opt *simulationv1.OptimizationConfig) float64 {
	if opt == nil {
		return DefaultOnlineTargetUtilLow
	}
	v := opt.GetTargetUtilLow()
	if v <= 0 {
		return DefaultOnlineTargetUtilLow
	}
	return v
}

// EffectiveOnlineTargetUtilHigh returns the configured high utilization bound, or the default when unset (<=0).
func EffectiveOnlineTargetUtilHigh(opt *simulationv1.OptimizationConfig) float64 {
	if opt == nil {
		return DefaultOnlineTargetUtilHigh
	}
	v := opt.GetTargetUtilHigh()
	if v <= 0 {
		return DefaultOnlineTargetUtilHigh
	}
	return v
}

// onlineEffectiveMinMaxHosts mirrors the online controller's effective host bounds after defaults.
func onlineEffectiveMinMaxHosts(opt *simulationv1.OptimizationConfig, initialHosts int) (minHosts, maxHosts int) {
	if initialHosts < 0 {
		initialHosts = 0
	}
	minHosts = int(opt.GetMinHosts())
	if minHosts <= 0 {
		minHosts = initialHosts
	}
	maxHosts = int(opt.GetMaxHosts())
	if maxHosts <= 0 {
		maxHosts = initialHosts
	}
	if maxHosts < minHosts {
		maxHosts = minHosts
	}
	return minHosts, maxHosts
}

const (
	warnCPUPrimaryHostScaleOutDisabled = "cpu-primary online optimization: max_hosts is not greater than initial host count; host scale-out is disabled"
	warnCPUPrimaryHostScaleInDisabled  = "cpu-primary online optimization: min_hosts is not less than initial host count; host scale-in is disabled"
)

// onlineCPUPrimaryHostScalingWarnings returns non-fatal diagnostics when CPU-primary online host
// scaling cannot occur because min/max host bounds collapse to the initial inventory.
func onlineCPUPrimaryHostScalingWarnings(opt *simulationv1.OptimizationConfig, initialHostCount int) []string {
	if opt == nil || !opt.GetOnline() {
		return nil
	}
	primary := strings.ToLower(strings.TrimSpace(opt.GetOptimizationTargetPrimary()))
	if primary == "" {
		primary = "p95_latency"
	}
	if primary != "cpu_utilization" {
		return nil
	}
	minH, maxH := onlineEffectiveMinMaxHosts(opt, initialHostCount)
	var out []string
	if maxH <= initialHostCount {
		out = append(out, warnCPUPrimaryHostScaleOutDisabled)
	}
	if minH >= initialHostCount {
		out = append(out, warnCPUPrimaryHostScaleInDisabled)
	}
	return out
}

// validateOnlinePrimaryUtilizationBand rejects impossible utilization bands for utilization-primary online runs.
func validateOnlinePrimaryUtilizationBand(opt *simulationv1.OptimizationConfig, primary string) error {
	if opt == nil || !opt.GetOnline() {
		return nil
	}
	if primary != "cpu_utilization" && primary != "memory_utilization" {
		return nil
	}
	lowRaw := opt.GetTargetUtilLow()
	highRaw := opt.GetTargetUtilHigh()
	if lowRaw < 0 || highRaw < 0 {
		return fmt.Errorf("online optimization: target_util_low and target_util_high must be >= 0")
	}
	if lowRaw > 1 || highRaw > 1 {
		return fmt.Errorf("online optimization: target_util_low and target_util_high must be <= 1")
	}
	effLow := EffectiveOnlineTargetUtilLow(opt)
	effHigh := EffectiveOnlineTargetUtilHigh(opt)
	if effLow >= effHigh {
		return fmt.Errorf("online optimization: require target_util_low < target_util_high (after defaults %g / %g); effective band is %g / %g",
			DefaultOnlineTargetUtilLow, DefaultOnlineTargetUtilHigh, effLow, effHigh)
	}
	return nil
}
