package simd

import (
	"fmt"
	"math"
	"strings"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
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
	warnCPUPrimaryHostCPUVertUpDisabled = "cpu-primary online optimization: max_host_cpu_cores defaults to initial host cores; host CPU vertical scale-up is disabled unless max_host_cpu_cores is raised above the scenario baseline"
	warnMemPrimaryHostMemVertUpDisabled = "memory-primary online optimization: max_host_memory_gb defaults to initial host memory; host memory vertical scale-up is disabled unless max_host_memory_gb is raised above the scenario baseline"
)

// ScenarioInitialHostCPUCores returns the first host's core count (>=1) for online defaults.
func ScenarioInitialHostCPUCores(sc *config.Scenario) int {
	if sc == nil || len(sc.Hosts) == 0 {
		return 1
	}
	c := sc.Hosts[0].Cores
	if c < 1 {
		return 1
	}
	return c
}

// ScenarioInitialHostMemoryGB returns the first host's memory GB (defaults to 16 when unset, matching resource.InitializeFromScenario).
func ScenarioInitialHostMemoryGB(sc *config.Scenario) int {
	if sc == nil || len(sc.Hosts) == 0 {
		return 16
	}
	gb := sc.Hosts[0].MemoryGB
	if gb <= 0 {
		return 16
	}
	return gb
}

// EffectiveOnlineMinHostCPUCores returns the configured floor or the scenario baseline when unset.
func EffectiveOnlineMinHostCPUCores(opt *simulationv1.OptimizationConfig, initialCores int) int {
	if opt == nil || opt.GetMinHostCpuCores() <= 0 {
		if initialCores < 1 {
			return 1
		}
		return initialCores
	}
	return int(opt.GetMinHostCpuCores())
}

// EffectiveOnlineMaxHostCPUCores returns the configured cap or the scenario baseline when unset.
func EffectiveOnlineMaxHostCPUCores(opt *simulationv1.OptimizationConfig, initialCores int) int {
	if opt == nil || opt.GetMaxHostCpuCores() <= 0 {
		if initialCores < 1 {
			return 1
		}
		return initialCores
	}
	return int(opt.GetMaxHostCpuCores())
}

// EffectiveOnlineMinHostMemoryGb returns the configured floor or the scenario baseline when unset.
func EffectiveOnlineMinHostMemoryGb(opt *simulationv1.OptimizationConfig, initialMemGB int) int {
	if opt == nil || opt.GetMinHostMemoryGb() <= 0 {
		if initialMemGB < 1 {
			return 1
		}
		return initialMemGB
	}
	return int(opt.GetMinHostMemoryGb())
}

// EffectiveOnlineMaxHostMemoryGb returns the configured cap or the scenario baseline when unset.
func EffectiveOnlineMaxHostMemoryGb(opt *simulationv1.OptimizationConfig, initialMemGB int) int {
	if opt == nil || opt.GetMaxHostMemoryGb() <= 0 {
		if initialMemGB < 1 {
			return 1
		}
		return initialMemGB
	}
	return int(opt.GetMaxHostMemoryGb())
}

// EffectiveOnlineHostCPUStepCores returns host_cpu_step_cores or derives from step_size.
func EffectiveOnlineHostCPUStepCores(opt *simulationv1.OptimizationConfig) int {
	if opt != nil && opt.GetHostCpuStepCores() > 0 {
		return int(opt.GetHostCpuStepCores())
	}
	step := 1.0
	if opt != nil {
		step = opt.GetStepSize()
	}
	if step <= 0 {
		step = 1.0
	}
	v := int(math.Ceil(step))
	if v < 1 {
		v = 1
	}
	return v
}

// EffectiveOnlineHostMemoryStepGb returns host_memory_step_gb or 1.
func EffectiveOnlineHostMemoryStepGb(opt *simulationv1.OptimizationConfig) int {
	if opt != nil && opt.GetHostMemoryStepGb() > 0 {
		return int(opt.GetHostMemoryStepGb())
	}
	return 1
}

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

// onlineHostVerticalCapacityWarnings surfaces non-fatal diagnostics when utilization-primary
// online runs default host vertical caps to the scenario baseline (no headroom until configured).
func onlineHostVerticalCapacityWarnings(opt *simulationv1.OptimizationConfig, sc *config.Scenario) []string {
	if opt == nil || !opt.GetOnline() || sc == nil || len(sc.Hosts) == 0 {
		return nil
	}
	primary := strings.ToLower(strings.TrimSpace(opt.GetOptimizationTargetPrimary()))
	if primary == "" {
		primary = "p95_latency"
	}
	initC := ScenarioInitialHostCPUCores(sc)
	initM := ScenarioInitialHostMemoryGB(sc)
	var out []string
	if primary == "cpu_utilization" && opt.GetMaxHostCpuCores() <= 0 {
		if EffectiveOnlineMaxHostCPUCores(opt, initC) <= initC {
			out = append(out, warnCPUPrimaryHostCPUVertUpDisabled)
		}
	}
	if primary == "memory_utilization" && opt.GetMaxHostMemoryGb() <= 0 {
		if EffectiveOnlineMaxHostMemoryGb(opt, initM) <= initM {
			out = append(out, warnMemPrimaryHostMemVertUpDisabled)
		}
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
