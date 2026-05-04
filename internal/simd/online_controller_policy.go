package simd

import (
	"errors"
	"strings"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"

	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
)

const (
	reasonCPUReplicaScaleUp     = "service CPU above target, scaled replicas up"
	reasonCPUHostScalePlacement = "service CPU above target, scaled out hosts for placement"
	reasonCPUHostScaleHot       = "host CPU above target, scaled out hosts"
	reasonCPUReplicaScaleDown   = "service CPU below target and guardrails safe, scaled replicas down"
	reasonCPUHostScaleIn        = "host CPU below target and guardrails safe, scaled in hosts"
	reasonCPUPrimaryHostCPUVertUp   = "cpu-primary: max host CPU above target, increased host CPU capacity"
	reasonCPUPrimaryHostCPUVertDown = "cpu-primary: max host CPU below threshold, decreased host CPU capacity"
	reasonMemPrimaryHostMemVertUp   = "memory-primary: max host memory above target, increased host memory capacity"
	reasonMemPrimaryHostMemVertDown = "memory-primary: max host memory below threshold, decreased host memory capacity"
)

func onlineOptReplay(opt *simulationv1.OptimizationConfig, tick *onlineCtrlTickInput, primary string, meta *onlineOptimizationStepMeta) *onlineOptimizationHistoryBundle {
	if meta == nil {
		return nil
	}
	return &onlineOptimizationHistoryBundle{
		Opt:              opt,
		Tick:             tick,
		PrimaryTargetCtl: primary,
		Meta:             meta,
	}
}

// cpuPrimaryScaleUpScratch records CPU-primary scale-up signals within one control tick.
type cpuPrimaryScaleUpScratch struct {
	capacityBlockedReplicaUp bool
	blockedServiceID         string
	blockedTargetReplicas    int
}

// onlineControllerPolicyBranch identifies which online policy implementation handles a tick.
// Phase 2 dispatches here so Phase 3 can specialize CPU/memory without further restructuring.
type onlineControllerPolicyBranch int

const (
	onlinePolicyP95 onlineControllerPolicyBranch = iota
	onlinePolicyCPU
	onlinePolicyMemory
)

func onlineControllerPolicyBranchFromPrimary(primary string) onlineControllerPolicyBranch {
	p := strings.ToLower(strings.TrimSpace(primary))
	switch p {
	case "cpu_utilization":
		return onlinePolicyCPU
	case "memory_utilization":
		return onlinePolicyMemory
	default:
		return onlinePolicyP95
	}
}

// onlineCtrlLoopState holds controller state that persists across control intervals.
type onlineCtrlLoopState struct {
	stepIndex                   int32
	lastScaleWall               time.Time
	stableRepDown               map[string]int
	stableHostScaleIn           int
	stableHostCPUDown           int
	stableHostMemDown           int
	stableVertCPUDown           map[string]int
	stableVertMemDown           map[string]int
	prevErrFrac                 float64
	stableCPUPrimaryHostScaleIn int
	stableCPUPrimaryHostVertCPUUp int
	stableMemoryPrimaryHostVertMemUp int
}

// onlineCtrlTickInput is a per-tick snapshot of metrics and tuning inputs for policy steps.
type onlineCtrlTickInput struct {
	runMetrics           *models.RunMetrics
	currentP95           float64
	targetP95            float64
	p95Guard             bool
	brokerPressure       map[string]brokerPressureSignal
	hostCount            int
	maxHostCPU           float64
	maxHostMem           float64
	stabTicks            int
	minReplicasCtl       int
	minCPUCtl            float64
	minMemCtl            float64
	memHeadroomCtl       float64
	scaleDownCPUMax      float64
	scaleDownMemMax      float64
	scaleDownHostCPUMax  float64
	scaleDownHostMemMax  float64
	initialHostCores     int
	initialHostMemGB     int
	minHosts             int
	maxHosts             int
	cpuHighThreshold     float64
	hostCPUHighThreshold float64
	minHostCPUCores      int
	maxHostCPUCores      int
	minHostMemGB         int
	maxHostMemGB         int
	hostCPUStepCores     int
	hostMemoryStepGB     int
}

// runP95PrimaryOnlineStep applies the latency-primary online scaling policy for one tick.
func (e *RunExecutor) runP95PrimaryOnlineStep(
	runID string,
	scenario *config.Scenario,
	opt *simulationv1.OptimizationConfig,
	rm *resource.Manager,
	state *scenarioState,
	loop *onlineCtrlLoopState,
	tick *onlineCtrlTickInput,
	primaryTargetCtl string,
) {
	e.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, primaryTargetCtl, false, nil)
}

// runCPUPrimaryOnlineStep applies the CPU-primary policy for one tick (Phase 3: CPU-driven
// service/host scale-up plus post-tick host placement path; shared service loop for guardrails).
func (e *RunExecutor) runCPUPrimaryOnlineStep(
	runID string,
	scenario *config.Scenario,
	opt *simulationv1.OptimizationConfig,
	rm *resource.Manager,
	state *scenarioState,
	loop *onlineCtrlLoopState,
	tick *onlineCtrlTickInput,
	primaryTargetCtl string,
) {
	scratch := &cpuPrimaryScaleUpScratch{}
	e.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, primaryTargetCtl, true, scratch)
}

// runMemoryPrimaryOnlineStep applies the memory-primary policy for one tick.
// Phase 2 delegates to the legacy unified step; Phase 3 will split memory-specific rules here.
func (e *RunExecutor) runMemoryPrimaryOnlineStep(
	runID string,
	scenario *config.Scenario,
	opt *simulationv1.OptimizationConfig,
	rm *resource.Manager,
	state *scenarioState,
	loop *onlineCtrlLoopState,
	tick *onlineCtrlTickInput,
	primaryTargetCtl string,
) {
	e.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, primaryTargetCtl, false, nil)
}

// runOnlineControllerPolicyStep contains host- and service-level scaling logic shared across
// online primary targets (behavior preserved from the pre-Phase-2 monolithic controller).
func (e *RunExecutor) runOnlineControllerPolicyStep(
	runID string,
	scenario *config.Scenario,
	opt *simulationv1.OptimizationConfig,
	rm *resource.Manager,
	state *scenarioState,
	loop *onlineCtrlLoopState,
	tick *onlineCtrlTickInput,
	primaryTargetCtl string,
	cpuPrimary bool,
	cpuScratch *cpuPrimaryScaleUpScratch,
) {
	_ = state // scenario/engine context reserved for target-specific policies (Phase 3+)
	if tick == nil || tick.runMetrics == nil || loop == nil {
		return
	}
	if cpuPrimary && cpuScratch == nil {
		cpuScratch = &cpuPrimaryScaleUpScratch{}
	}

	runMetrics := tick.runMetrics
	currentP95 := tick.currentP95
	targetP95 := tick.targetP95
	p95Guard := tick.p95Guard
	brokerPressure := tick.brokerPressure
	hostCount := tick.hostCount
	maxHostCPU := tick.maxHostCPU
	maxHostMem := tick.maxHostMem
	stabTicks := tick.stabTicks
	minReplicasCtl := tick.minReplicasCtl
	minCPUCtl := tick.minCPUCtl
	minMemCtl := tick.minMemCtl
	memHeadroomCtl := tick.memHeadroomCtl
	scaleDownCPUMax := tick.scaleDownCPUMax
	scaleDownMemMax := tick.scaleDownMemMax
	scaleDownHostCPUMax := tick.scaleDownHostCPUMax
	scaleDownHostMemMax := tick.scaleDownHostMemMax
	minHosts := tick.minHosts
	maxHosts := tick.maxHosts
	cpuHighThreshold := tick.cpuHighThreshold
	hostCPUHighThreshold := tick.hostCPUHighThreshold

	const scaleReasonP95HostOut = "p95 above target, host CPU hot, scaled out hosts"
	const scaleReasonP95HostCap = "p95 above target, hosts at max, increased host CPU capacity"
	const scaleReasonP95HostIn = "p95 and host utilization low, scaled in hosts"
	const scaleReasonHostCPU = "host utilization low, decreased host CPU capacity"
	const scaleReasonHostMem = "host memory utilization low, decreased host memory capacity"

	if p95Guard && currentP95 > targetP95*1.05 && hostCount > 0 {
		// Horizontal host scale-out remains P95-gated for non-CPU-primary; CPU-primary uses
		// cpuPrimaryPostServiceHostScaleOutAndRetry for host adds without requiring P95 over target.
		if !cpuPrimary && hostCount < maxHosts && maxHostCPU >= hostCPUHighThreshold {
			prevConfig, _ := e.GetRunConfiguration(runID)
			if err := rm.ScaleOutHosts(hostCount + 1); err != nil {
				logger.Error("online controller failed to scale out hosts",
					"run_id", runID,
					"current_hosts", hostCount,
					"target_hosts", hostCount+1,
					"error", err)
			} else {
				logger.Info("online controller scaled out hosts",
					"run_id", runID,
					"previous_hosts", hostCount,
					"new_hosts", rm.HostCount(),
					"max_hosts", maxHosts,
					"max_host_cpu_utilization", maxHostCPU)
				if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
					loop.stepIndex++
					e.recordOptimizationStep(runID, loop.stepIndex, targetP95, currentP95,
						scaleReasonP95HostOut,
						prevConfig, currConfig,
						onlineOptReplay(opt, tick, primaryTargetCtl, &onlineOptimizationStepMeta{
							Action:              "host_scale_out",
							DecisionMetric:      "max_host_cpu_utilization",
							DecisionMetricValue: maxHostCPU,
						}))
				}
			}
		} else if hostCount >= maxHosts && maxHostCPU >= hostCPUHighThreshold {
			prevConfig, _ := e.GetRunConfiguration(runID)
			changes, errInc := rm.IncreaseOnlineHostCPUCapacity(tick.hostCPUStepCores, tick.maxHostCPUCores)
			if errInc != nil {
				logger.Debug("online controller host CPU vertical scale-up skipped",
					"run_id", runID, "error", errInc)
			} else if len(changes) > 0 {
				logger.Info("online controller increased host capacity",
					"run_id", runID,
					"cpu_step", tick.hostCPUStepCores,
					"host_count", rm.HostCount(),
					"max_hosts", maxHosts,
					"max_host_cpu_utilization", maxHostCPU)
				if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
					loop.stepIndex++
					meta := &onlineOptimizationStepMeta{
						Action:              "host_cpu_scale_up",
						DecisionMetric:      "max_host_cpu_utilization",
						DecisionMetricValue: maxHostCPU,
					}
					if primaryTargetCtl == "cpu_utilization" || primaryTargetCtl == "memory_utilization" {
						mhv := maxHostCPU
						meta.ObjectiveScoreOverride = &mhv
					}
					e.recordOptimizationStep(runID, loop.stepIndex, targetP95, currentP95,
						scaleReasonP95HostCap,
						prevConfig, currConfig,
						onlineOptReplay(opt, tick, primaryTargetCtl, meta))
				}
			}
		}
	}

	if !cpuPrimary {
		hostScaleInCond := p95Guard && scaleDownHostCPUMax > 0 && currentP95 < targetP95*0.7 && hostCount > minHosts && maxHostCPU < scaleDownHostCPUMax
		if hostScaleInCond {
			loop.stableHostScaleIn++
		} else {
			loop.stableHostScaleIn = 0
		}
		if hostScaleInCond && loop.stableHostScaleIn >= stabTicks {
			prevConfig, _ := e.GetRunConfiguration(runID)
			if err := rm.ScaleInHosts(hostCount - 1); err != nil {
				logger.Debug("online controller scale-in hosts skipped",
					"run_id", runID,
					"host_count", hostCount,
					"error", err)
			} else if rm.HostCount() < hostCount {
				loop.stableHostScaleIn = 0
				logger.Info("online controller scaled in hosts",
					"run_id", runID,
					"previous_hosts", hostCount,
					"new_hosts", rm.HostCount(),
					"min_hosts", minHosts)
				if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
					loop.stepIndex++
					e.recordOptimizationStep(runID, loop.stepIndex, targetP95, currentP95,
						scaleReasonP95HostIn,
						prevConfig, currConfig,
						onlineOptReplay(opt, tick, primaryTargetCtl, &onlineOptimizationStepMeta{
							Action:              "host_scale_in",
							DecisionMetric:      "max_host_cpu_utilization",
							DecisionMetricValue: maxHostCPU,
						}))
				}
			}
		}
	}

	minFleetCPU := minFleetHostCPUCores(rm)
	hostVertDownGuard := primaryTargetCtl != "cpu_utilization" && primaryTargetCtl != "memory_utilization"
	if primaryTargetCtl == "cpu_utilization" || primaryTargetCtl == "memory_utilization" {
		hostVertDownGuard = cpuPrimaryHostScaleInSafe(runMetrics, currentP95, targetP95, p95Guard, loop.prevErrFrac, brokerPressure)
	}
	topologyBlocked, _ := onlineTopologyGuard(runMetrics, scenario, runID, rm, opt, hostCount)
	hostCPUDownCond := scaleDownHostCPUMax > 0 && hostCount >= minHosts && maxHostCPU < scaleDownHostCPUMax &&
		minFleetCPU > tick.minHostCPUCores && hostVertDownGuard && !topologyBlocked
	if hostCPUDownCond {
		loop.stableHostCPUDown++
	} else {
		loop.stableHostCPUDown = 0
	}
	if hostCPUDownCond && loop.stableHostCPUDown >= stabTicks {
		prevConfig, _ := e.GetRunConfiguration(runID)
		changes, errDec := rm.DecreaseOnlineHostCPUCapacity(tick.hostCPUStepCores, tick.minHostCPUCores)
		if errDec != nil {
			logger.Debug("online controller decrease host capacity skipped",
				"run_id", runID,
				"error", errDec)
		} else if len(changes) > 0 {
			loop.stableHostCPUDown = 0
			reason := scaleReasonHostCPU
			if primaryTargetCtl == "cpu_utilization" {
				reason = reasonCPUPrimaryHostCPUVertDown
			}
			logger.Info("online controller decreased host capacity",
				"run_id", runID,
				"cpu_step", tick.hostCPUStepCores,
				"host_count", rm.HostCount())
			if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
				loop.stepIndex++
				meta := &onlineOptimizationStepMeta{
					Action:              "host_cpu_scale_down",
					DecisionMetric:      "max_host_cpu_utilization",
					DecisionMetricValue: maxHostCPU,
				}
				if primaryTargetCtl == "cpu_utilization" || primaryTargetCtl == "memory_utilization" {
					mhv := maxHostCPU
					meta.ObjectiveScoreOverride = &mhv
				}
				e.recordOptimizationStep(runID, loop.stepIndex, targetP95, currentP95,
					reason,
					prevConfig, currConfig,
					onlineOptReplay(opt, tick, primaryTargetCtl, meta))
			}
		} else {
			loop.stableHostCPUDown = 0
		}
	}

	minFleetMem := minFleetHostMemoryGB(rm)
	hostMemDownCond := scaleDownHostMemMax > 0 && hostCount >= minHosts && maxHostMem < scaleDownHostMemMax &&
		minFleetMem > tick.minHostMemGB && hostVertDownGuard && !topologyBlocked
	if hostMemDownCond {
		loop.stableHostMemDown++
	} else {
		loop.stableHostMemDown = 0
	}
	if hostMemDownCond && loop.stableHostMemDown >= stabTicks {
		prevConfig, _ := e.GetRunConfiguration(runID)
		changes, errDec := rm.DecreaseOnlineHostMemoryCapacity(tick.hostMemoryStepGB, tick.minHostMemGB)
		if errDec != nil {
			logger.Debug("online controller decrease host memory skipped",
				"run_id", runID,
				"error", errDec)
		} else if len(changes) > 0 {
			loop.stableHostMemDown = 0
			reason := scaleReasonHostMem
			if primaryTargetCtl == "memory_utilization" {
				reason = reasonMemPrimaryHostMemVertDown
			}
			logger.Info("online controller decreased host memory capacity",
				"run_id", runID,
				"memory_gb_step", tick.hostMemoryStepGB,
				"host_count", rm.HostCount())
			if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
				loop.stepIndex++
				meta := &onlineOptimizationStepMeta{
					Action:              "host_memory_scale_down",
					DecisionMetric:      "max_host_memory_utilization",
					DecisionMetricValue: maxHostMem,
				}
				if primaryTargetCtl == "cpu_utilization" || primaryTargetCtl == "memory_utilization" {
					mhm := maxHostMem
					meta.ObjectiveScoreOverride = &mhm
				}
				e.recordOptimizationStep(runID, loop.stepIndex, targetP95, currentP95,
					reason,
					prevConfig, currConfig,
					onlineOptReplay(opt, tick, primaryTargetCtl, meta))
			}
		} else {
			loop.stableHostMemDown = 0
		}
	}

	p95OkForDown := !p95Guard || currentP95 <= targetP95*1.05
	step := int(opt.StepSize)
	if step < 1 {
		step = 1
	}
	cpuStep := opt.StepSize
	if cpuStep <= 0 {
		cpuStep = 1.0
	}

	for i := range scenario.Services {
		svc := &scenario.Services[i]
		currentReplicas := rm.ActiveReplicas(svc.ID)
		if currentReplicas < 1 {
			currentReplicas = 1
		}

		newReplicas := currentReplicas

		instances := rm.GetInstancesForService(svc.ID)
		currentCores := resource.DefaultInstanceCPUCores
		currentMemMB := resource.DefaultInstanceMemoryMB
		var routable *resource.ServiceInstance
		for _, inst := range instances {
			if inst.IsRoutable() {
				routable = inst
				break
			}
		}
		if routable != nil {
			currentCores = routable.CPUCores()
			currentMemMB = routable.MemoryMB()
		} else if len(instances) > 0 {
			currentCores = instances[0].CPUCores()
			currentMemMB = instances[0].MemoryMB()
		}
		newCPUCores := currentCores
		newMemMBVert := currentMemMB

		var svcCPUUtil, svcMemUtil float64
		if runMetrics.ServiceMetrics != nil {
			if sm := runMetrics.ServiceMetrics[svc.ID]; sm != nil {
				svcCPUUtil = sm.CPUUtilization
				svcMemUtil = sm.MemoryUtilization
			}
		}

		primaryTarget := primaryTargetCtl
		targetUtilHigh := EffectiveOnlineTargetUtilHigh(opt)
		targetUtilLow := EffectiveOnlineTargetUtilLow(opt)

		scaledVertically := false

		if primaryTarget == "cpu_utilization" || primaryTarget == "memory_utilization" {
			util := svcCPUUtil
			if primaryTarget == "memory_utilization" {
				util = svcMemUtil
			}
			switch {
			case brokerPressure[svc.ID].HasBacklog || brokerPressure[svc.ID].HasInFlight || brokerPressure[svc.ID].MaxOldestAgeMs > 0:
				if config.ServiceAllowsVerticalCPU(svc) && svcCPUUtil >= cpuHighThreshold {
					newCPUCores = currentCores + cpuStep
					scaledVertically = true
				} else if config.ServiceAllowsHorizontalScaling(svc) {
					newReplicas = currentReplicas + step
				}
			case util > targetUtilHigh:
				if config.ServiceAllowsVerticalCPU(svc) && svcCPUUtil >= cpuHighThreshold {
					newCPUCores = currentCores + cpuStep
					scaledVertically = true
				} else if config.ServiceAllowsHorizontalScaling(svc) {
					newReplicas = currentReplicas + step
				}
			case util < targetUtilLow && currentReplicas > 1:
				downOk := p95OkForDown && allowScaleDownReplicas(svcCPUUtil, svcMemUtil, scaleDownCPUMax, scaleDownMemMax)
				if cpuPrimary && primaryTarget == "cpu_utilization" {
					downOk = downOk && cpuPrimaryScaleDownSafe(runMetrics, currentP95, targetP95, p95Guard, loop.prevErrFrac, brokerPressure, svc.ID, rm)
				}
				if downOk && config.ServiceAllowsHorizontalScaling(svc) {
					newReplicas = currentReplicas - 1
				}
			}
		} else {
			switch {
			case brokerPressure[svc.ID].HasBacklog || brokerPressure[svc.ID].HasInFlight || brokerPressure[svc.ID].MaxOldestAgeMs > 0:
				if config.ServiceAllowsVerticalCPU(svc) && svcCPUUtil >= cpuHighThreshold {
					newCPUCores = currentCores + cpuStep
					scaledVertically = true
				} else if config.ServiceAllowsHorizontalScaling(svc) {
					newReplicas = currentReplicas + step
				}
			case p95Guard && currentP95 > targetP95*1.05:
				if config.ServiceAllowsVerticalCPU(svc) && svcCPUUtil >= cpuHighThreshold {
					newCPUCores = currentCores + cpuStep
					scaledVertically = true
				} else if config.ServiceAllowsHorizontalScaling(svc) {
					newReplicas = currentReplicas + step
				}
			case p95Guard && currentP95 < targetP95*0.7 && currentReplicas > 1:
				if config.ServiceAllowsHorizontalScaling(svc) && allowScaleDownReplicas(svcCPUUtil, svcMemUtil, scaleDownCPUMax, scaleDownMemMax) {
					newReplicas = currentReplicas - 1
				}
			}
		}

		vertDownGuardOk := p95OkForDown && !onlineScaleDownGuard(rm, runMetrics, svc.ID, targetP95, loop.prevErrFrac, brokerPressure)
		if cpuPrimary && primaryTarget == "cpu_utilization" {
			vertDownGuardOk = cpuPrimaryScaleDownSafe(runMetrics, currentP95, targetP95, p95Guard, loop.prevErrFrac, brokerPressure, svc.ID, rm)
		}
		wantVertCPU := primaryTarget == "cpu_utilization" && config.ServiceAllowsVerticalCPU(svc) && svcCPUUtil < targetUtilLow && vertDownGuardOk && newReplicas >= currentReplicas &&
			allowScaleDownReplicas(svcCPUUtil, svcMemUtil, scaleDownCPUMax, scaleDownMemMax)
		var targetVertCPU float64
		if wantVertCPU {
			nc := currentCores - cpuStep
			if minCPUCtl > 0 && nc < minCPUCtl {
				nc = minCPUCtl
			}
			if nc+1e-9 < currentCores {
				targetVertCPU = nc
			} else {
				wantVertCPU = false
			}
		}
		if wantVertCPU {
			loop.stableVertCPUDown[svc.ID]++
		} else {
			loop.stableVertCPUDown[svc.ID] = 0
		}
		if wantVertCPU && loop.stableVertCPUDown[svc.ID] >= stabTicks && targetVertCPU > 0 {
			newCPUCores = targetVertCPU
			scaledVertically = true
			loop.stableVertCPUDown[svc.ID] = 0
		}

		wantVertMem := primaryTarget == "memory_utilization" && config.ServiceAllowsVerticalMemory(svc) && svcMemUtil < targetUtilLow && p95OkForDown && newReplicas >= currentReplicas &&
			allowScaleDownReplicas(svcCPUUtil, svcMemUtil, scaleDownCPUMax, scaleDownMemMax) &&
			!onlineScaleDownGuard(rm, runMetrics, svc.ID, targetP95, loop.prevErrFrac, brokerPressure)
		var targetVertMem float64
		if wantVertMem {
			nm := currentMemMB - float64(step)*128
			if minMemCtl > 0 && nm < minMemCtl {
				nm = minMemCtl
			}
			if nm+1e-9 < currentMemMB {
				targetVertMem = nm
			} else {
				wantVertMem = false
			}
		}
		if wantVertMem {
			loop.stableVertMemDown[svc.ID]++
		} else {
			loop.stableVertMemDown[svc.ID] = 0
		}
		if wantVertMem && loop.stableVertMemDown[svc.ID] >= stabTicks && targetVertMem > 0 {
			newMemMBVert = targetVertMem
			loop.stableVertMemDown[svc.ID] = 0
		}

		if newReplicas < currentReplicas {
			loop.stableRepDown[svc.ID]++
			if loop.stableRepDown[svc.ID] < stabTicks {
				newReplicas = currentReplicas
			}
		} else {
			loop.stableRepDown[svc.ID] = 0
		}
		replicaDownBlocked := onlineScaleDownGuard(rm, runMetrics, svc.ID, targetP95, loop.prevErrFrac, brokerPressure)
		if cpuPrimary && primaryTarget == "cpu_utilization" {
			replicaDownBlocked = !cpuPrimaryScaleDownSafe(runMetrics, currentP95, targetP95, p95Guard, loop.prevErrFrac, brokerPressure, svc.ID, rm)
		}
		if newReplicas < currentReplicas && replicaDownBlocked {
			newReplicas = currentReplicas
			loop.stableRepDown[svc.ID] = 0
		}
		if newReplicas < minReplicasCtl {
			newReplicas = minReplicasCtl
		}

		if scaledVertically && newCPUCores != currentCores {
			prevConfig, _ := e.GetRunConfiguration(runID)
			if err := e.UpdateServiceResources(runID, svc.ID, newCPUCores, 0); err != nil {
				logger.Error("online controller failed to update service resources",
					"run_id", runID,
					"service_id", svc.ID,
					"old_cpu_cores", currentCores,
					"new_cpu_cores", newCPUCores,
					"error", err)
				if config.ServiceAllowsHorizontalScaling(svc) {
					if primaryTargetCtl == "cpu_utilization" || primaryTargetCtl == "memory_utilization" {
						newReplicas = currentReplicas + step
					} else if p95Guard && currentP95 > targetP95*1.05 {
						newReplicas = currentReplicas + step
					}
				}
			} else {
				logger.Info("online controller updated service resources",
					"run_id", runID,
					"service_id", svc.ID,
					"old_cpu_cores", currentCores,
					"new_cpu_cores", newCPUCores,
					"p95_ms", currentP95,
					"target_p95_ms", targetP95,
					"cpu_utilization", svcCPUUtil)
				if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
					loop.stepIndex++
					obj := svcCPUUtil
					e.recordOptimizationStep(runID, loop.stepIndex, targetP95, currentP95,
						"p95 above target, service CPU hot, scaled CPU cores",
						prevConfig, currConfig,
						onlineOptReplay(opt, tick, primaryTargetCtl, &onlineOptimizationStepMeta{
							Action:                 "service_cpu_scale_up",
							DecisionServiceID:      svc.ID,
							DecisionMetric:         "service_cpu_utilization",
							DecisionMetricValue:    svcCPUUtil,
							ObjectiveScoreOverride: &obj,
						}))
				}
				continue
			}
		}

		if newMemMBVert+1e-9 < currentMemMB {
			prevConfig, _ := e.GetRunConfiguration(runID)
			if err := e.UpdateServiceResourcesWithHeadroom(runID, svc.ID, 0, newMemMBVert, memHeadroomCtl); err != nil {
				logger.Debug("online controller memory downscale skipped",
					"run_id", runID,
					"service_id", svc.ID,
					"error", err)
			} else {
				logger.Info("online controller decreased service memory",
					"run_id", runID,
					"service_id", svc.ID,
					"old_memory_mb", currentMemMB,
					"new_memory_mb", newMemMBVert)
				if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
					loop.stepIndex++
					memObj := svcMemUtil
					e.recordOptimizationStep(runID, loop.stepIndex, targetP95, currentP95,
						"memory utilization low, decreased per-instance memory",
						prevConfig, currConfig,
						onlineOptReplay(opt, tick, primaryTargetCtl, &onlineOptimizationStepMeta{
							Action:                 "service_memory_scale_down",
							DecisionServiceID:      svc.ID,
							DecisionMetric:         "service_memory_utilization",
							DecisionMetricValue:    svcMemUtil,
							ObjectiveScoreOverride: &memObj,
						}))
				}
				continue
			}
		}

		if newReplicas != currentReplicas {
			prevConfig, _ := e.GetRunConfiguration(runID)
			if err := e.UpdateServiceReplicas(runID, svc.ID, newReplicas); err != nil {
				logger.Error("online controller failed to update replicas",
					"run_id", runID,
					"service_id", svc.ID,
					"old", currentReplicas,
					"new", newReplicas,
					"error", err)
				if cpuPrimary && cpuScratch != nil && newReplicas > currentReplicas && onlineReplicaScaleUpCapacityBlocked(err) {
					if !cpuScratch.capacityBlockedReplicaUp {
						cpuScratch.capacityBlockedReplicaUp = true
						cpuScratch.blockedServiceID = svc.ID
						cpuScratch.blockedTargetReplicas = newReplicas
					}
				}
			} else {
				logger.Info("online controller updated replicas",
					"run_id", runID,
					"service_id", svc.ID,
					"old", currentReplicas,
					"new", newReplicas,
					"p95_ms", currentP95,
					"target_p95_ms", targetP95,
					"cpu_utilization", svcCPUUtil,
					"memory_utilization", svcMemUtil)
				if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
					reason := "p95 above target, scaled replicas up"
					if newReplicas < currentReplicas {
						reason = "p95 below target and utilization low, scaled replicas down"
						if cpuPrimary && primaryTarget == "cpu_utilization" {
							reason = reasonCPUReplicaScaleDown
						} else if primaryTarget == "cpu_utilization" || primaryTarget == "memory_utilization" {
							reason = "utilization below target and P95 ok, scaled replicas down"
						}
					} else if primaryTarget == "cpu_utilization" || primaryTarget == "memory_utilization" {
						if cpuPrimary && primaryTarget == "cpu_utilization" {
							reason = reasonCPUReplicaScaleUp
						} else {
							reason = "utilization above target, scaled replicas up"
						}
					}
					loop.stepIndex++
					var action, dMetric string
					var dVal, objScr float64
					var objPtr *float64
					if newReplicas > currentReplicas {
						action = "service_scale_out"
					} else {
						action = "service_scale_in"
					}
					switch primaryTarget {
					case "cpu_utilization":
						dMetric = "service_cpu_utilization"
						dVal = svcCPUUtil
						objScr = svcCPUUtil
						objPtr = &objScr
					case "memory_utilization":
						dMetric = "service_memory_utilization"
						dVal = svcMemUtil
						objScr = svcMemUtil
						objPtr = &objScr
					default:
						dMetric = "latency_p95_ms"
						dVal = currentP95
						objPtr = nil
					}
					e.recordOptimizationStep(runID, loop.stepIndex, targetP95, currentP95,
						reason, prevConfig, currConfig,
						onlineOptReplay(opt, tick, primaryTargetCtl, &onlineOptimizationStepMeta{
							Action:                 action,
							DecisionServiceID:      svc.ID,
							DecisionMetric:         dMetric,
							DecisionMetricValue:    dVal,
							ObjectiveScoreOverride: objPtr,
						}))
				}
			}
		}
	}

	if cpuPrimary {
		e.cpuPrimaryPostServiceHostScaleIn(runID, scenario, opt, rm, loop, tick)
	}
	if cpuPrimary && cpuScratch != nil {
		e.cpuPrimaryPostServiceHostScaleOutAndRetry(runID, scenario, opt, rm, loop, tick, cpuScratch)
	}
	if cpuPrimary && cpuScratch != nil {
		e.cpuPrimaryTryHostCPUVerticalScaleUp(runID, scenario, opt, rm, loop, tick, cpuScratch)
	}
	if strings.EqualFold(strings.TrimSpace(primaryTargetCtl), "memory_utilization") {
		e.memoryPrimaryTryHostMemoryVerticalScaleUp(runID, scenario, opt, rm, loop, tick)
	}
}

func minFleetHostCPUCores(rm *resource.Manager) int {
	if rm == nil {
		return 0
	}
	first := true
	m := 0
	for _, id := range rm.HostIDs() {
		if h, ok := rm.GetHost(id); ok {
			c := h.CPUCores()
			if first || c < m {
				m = c
				first = false
			}
		}
	}
	if first {
		return 0
	}
	return m
}

func minFleetHostMemoryGB(rm *resource.Manager) int {
	if rm == nil {
		return 0
	}
	first := true
	m := 0
	for _, id := range rm.HostIDs() {
		if h, ok := rm.GetHost(id); ok {
			g := h.MemoryGB()
			if first || g < m {
				m = g
				first = false
			}
		}
	}
	if first {
		return 0
	}
	return m
}

func memoryPrimaryAnyHotServiceMem(scenario *config.Scenario, runMetrics *models.RunMetrics, targetUtilHigh float64) bool {
	if scenario == nil || runMetrics == nil || runMetrics.ServiceMetrics == nil {
		return false
	}
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		if strings.HasPrefix(strings.ToLower(svc.ID), "client") {
			continue
		}
		sm := runMetrics.ServiceMetrics[svc.ID]
		if sm != nil && sm.MemoryUtilization > targetUtilHigh {
			return true
		}
	}
	return false
}

func (e *RunExecutor) cpuPrimaryTryHostCPUVerticalScaleUp(
	runID string,
	scenario *config.Scenario,
	opt *simulationv1.OptimizationConfig,
	rm *resource.Manager,
	loop *onlineCtrlLoopState,
	tick *onlineCtrlTickInput,
	cpuScratch *cpuPrimaryScaleUpScratch,
) {
	if rm == nil || tick == nil || loop == nil || opt == nil || cpuScratch == nil || scenario == nil {
		return
	}
	th := EffectiveOnlineTargetUtilHigh(opt)
	hostCount := rm.HostCount()
	if hostCount < tick.maxHosts {
		loop.stableCPUPrimaryHostVertCPUUp = 0
		return
	}
	maxHostCPU := rm.MaxHostCPUUtilization()
	if maxHostCPU <= th {
		loop.stableCPUPrimaryHostVertCPUUp = 0
		return
	}
	pressure := cpuPrimaryAnyHotServiceCPU(scenario, tick.runMetrics, th) ||
		onlineAnyBrokerPressure(tick.brokerPressure) ||
		cpuScratch.capacityBlockedReplicaUp
	if !pressure {
		loop.stableCPUPrimaryHostVertCPUUp = 0
		return
	}
	headroom := false
	for _, hid := range rm.HostIDs() {
		if h, ok := rm.GetHost(hid); ok && h.CPUCores() < tick.maxHostCPUCores {
			headroom = true
			break
		}
	}
	if !headroom {
		loop.stableCPUPrimaryHostVertCPUUp = 0
		return
	}
	stab := tick.stabTicks
	if stab < 1 {
		stab = 1
	}
	loop.stableCPUPrimaryHostVertCPUUp++
	if loop.stableCPUPrimaryHostVertCPUUp < stab {
		return
	}
	prevConfig, _ := e.GetRunConfiguration(runID)
	changes, err := rm.IncreaseOnlineHostCPUCapacity(tick.hostCPUStepCores, tick.maxHostCPUCores)
	if err != nil {
		logger.Debug("online controller cpu-primary host CPU vertical scale-up skipped",
			"run_id", runID, "error", err)
		loop.stableCPUPrimaryHostVertCPUUp = 0
		return
	}
	if len(changes) == 0 {
		loop.stableCPUPrimaryHostVertCPUUp = 0
		return
	}
	loop.stableCPUPrimaryHostVertCPUUp = 0
	logger.Info("online controller cpu-primary increased host CPU capacity",
		"run_id", runID,
		"cpu_step", tick.hostCPUStepCores,
		"max_host_cpu_utilization", maxHostCPU)
	if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
		loop.stepIndex++
		mhv := maxHostCPU
		e.recordOptimizationStep(runID, loop.stepIndex, tick.targetP95, tick.currentP95,
			reasonCPUPrimaryHostCPUVertUp, prevConfig, currConfig,
			onlineOptReplay(opt, tick, "cpu_utilization", &onlineOptimizationStepMeta{
				Action:                 "host_cpu_scale_up",
				DecisionMetric:         "max_host_cpu_utilization",
				DecisionMetricValue:    maxHostCPU,
				ObjectiveScoreOverride: &mhv,
			}))
	}
}

func (e *RunExecutor) memoryPrimaryTryHostMemoryVerticalScaleUp(
	runID string,
	scenario *config.Scenario,
	opt *simulationv1.OptimizationConfig,
	rm *resource.Manager,
	loop *onlineCtrlLoopState,
	tick *onlineCtrlTickInput,
) {
	if rm == nil || tick == nil || loop == nil || opt == nil || scenario == nil {
		return
	}
	th := EffectiveOnlineTargetUtilHigh(opt)
	hostCount := rm.HostCount()
	if hostCount < tick.maxHosts {
		loop.stableMemoryPrimaryHostVertMemUp = 0
		return
	}
	maxHostMem := rm.MaxHostMemoryUtilization()
	if maxHostMem <= th {
		loop.stableMemoryPrimaryHostVertMemUp = 0
		return
	}
	pressure := memoryPrimaryAnyHotServiceMem(scenario, tick.runMetrics, th) || onlineAnyBrokerPressure(tick.brokerPressure)
	if !pressure {
		loop.stableMemoryPrimaryHostVertMemUp = 0
		return
	}
	headroom := false
	for _, hid := range rm.HostIDs() {
		if h, ok := rm.GetHost(hid); ok && h.MemoryGB() < tick.maxHostMemGB {
			headroom = true
			break
		}
	}
	if !headroom {
		loop.stableMemoryPrimaryHostVertMemUp = 0
		return
	}
	stab := tick.stabTicks
	if stab < 1 {
		stab = 1
	}
	loop.stableMemoryPrimaryHostVertMemUp++
	if loop.stableMemoryPrimaryHostVertMemUp < stab {
		return
	}
	prevConfig, _ := e.GetRunConfiguration(runID)
	changes, err := rm.IncreaseOnlineHostMemoryCapacity(tick.hostMemoryStepGB, tick.maxHostMemGB)
	if err != nil {
		logger.Debug("online controller memory-primary host memory vertical scale-up skipped",
			"run_id", runID, "error", err)
		loop.stableMemoryPrimaryHostVertMemUp = 0
		return
	}
	if len(changes) == 0 {
		loop.stableMemoryPrimaryHostVertMemUp = 0
		return
	}
	loop.stableMemoryPrimaryHostVertMemUp = 0
	logger.Info("online controller memory-primary increased host memory capacity",
		"run_id", runID,
		"memory_gb_step", tick.hostMemoryStepGB,
		"max_host_memory_utilization", maxHostMem)
	if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
		loop.stepIndex++
		mhm := maxHostMem
		e.recordOptimizationStep(runID, loop.stepIndex, tick.targetP95, tick.currentP95,
			reasonMemPrimaryHostMemVertUp, prevConfig, currConfig,
			onlineOptReplay(opt, tick, "memory_utilization", &onlineOptimizationStepMeta{
				Action:                 "host_memory_scale_up",
				DecisionMetric:         "max_host_memory_utilization",
				DecisionMetricValue:    maxHostMem,
				ObjectiveScoreOverride: &mhm,
			}))
	}
}

func (e *RunExecutor) cpuPrimaryPostServiceHostScaleIn(
	runID string,
	scenario *config.Scenario,
	opt *simulationv1.OptimizationConfig,
	rm *resource.Manager,
	loop *onlineCtrlLoopState,
	tick *onlineCtrlTickInput,
) {
	if rm == nil || tick == nil || loop == nil || scenario == nil {
		return
	}

	targetUtilLow := EffectiveOnlineTargetUtilLow(opt)
	targetUtilHigh := EffectiveOnlineTargetUtilHigh(opt)

	hostCount := rm.HostCount()
	if hostCount <= tick.minHosts {
		loop.stableCPUPrimaryHostScaleIn = 0
		return
	}

	maxHostCPU := rm.MaxHostCPUUtilization()
	var hostCold bool
	if tick.scaleDownHostCPUMax > 0 {
		hostCold = maxHostCPU < tick.scaleDownHostCPUMax
	} else {
		hostCold = maxHostCPU < targetUtilLow
	}

	runMetrics := tick.runMetrics
	stabTicks := tick.stabTicks
	if stabTicks < 1 {
		stabTicks = 1
	}

	noHotService := !cpuPrimaryAnyHotServiceCPU(scenario, runMetrics, targetUtilHigh)
	cond := hostCold &&
		cpuPrimaryHostScaleInSafe(runMetrics, tick.currentP95, tick.targetP95, tick.p95Guard, loop.prevErrFrac, tick.brokerPressure) &&
		noHotService

	if !cond {
		loop.stableCPUPrimaryHostScaleIn = 0
		return
	}
	loop.stableCPUPrimaryHostScaleIn++
	if loop.stableCPUPrimaryHostScaleIn < stabTicks {
		return
	}

	prevConfig, _ := e.GetRunConfiguration(runID)
	if err := rm.ScaleInHosts(hostCount - 1); err != nil {
		logger.Debug("online controller cpu-primary scale-in hosts skipped",
			"run_id", runID,
			"host_count", hostCount,
			"error", err)
		loop.stableCPUPrimaryHostScaleIn = 0
		return
	}
	if rm.HostCount() >= hostCount {
		loop.stableCPUPrimaryHostScaleIn = 0
		return
	}
	loop.stableCPUPrimaryHostScaleIn = 0
	logger.Info("online controller cpu-primary scaled in hosts",
		"run_id", runID,
		"previous_hosts", hostCount,
		"new_hosts", rm.HostCount(),
		"min_hosts", tick.minHosts)
	if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
		loop.stepIndex++
		e.recordOptimizationStep(runID, loop.stepIndex, tick.targetP95, tick.currentP95,
			reasonCPUHostScaleIn, prevConfig, currConfig,
			onlineOptReplay(opt, tick, "cpu_utilization", &onlineOptimizationStepMeta{
				Action:              "host_scale_in",
				DecisionMetric:      "max_host_cpu_utilization",
				DecisionMetricValue: maxHostCPU,
			}))
	}
}

func onlineReplicaScaleUpCapacityBlocked(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, resource.ErrPlacementInfeasible) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no host has capacity") ||
		strings.Contains(msg, "no hosts available")
}

func cpuPrimaryAnyHotServiceCPU(scenario *config.Scenario, runMetrics *models.RunMetrics, targetUtilHigh float64) bool {
	if scenario == nil || runMetrics == nil || runMetrics.ServiceMetrics == nil {
		return false
	}
	for i := range scenario.Services {
		svc := &scenario.Services[i]
		if strings.HasPrefix(strings.ToLower(svc.ID), "client") {
			continue
		}
		sm := runMetrics.ServiceMetrics[svc.ID]
		if sm != nil && sm.CPUUtilization > targetUtilHigh {
			return true
		}
	}
	return false
}

func onlineAnyBrokerPressure(pressure map[string]brokerPressureSignal) bool {
	for _, p := range pressure {
		if p.HasBacklog || p.HasInFlight || p.MaxOldestAgeMs > 0 {
			return true
		}
	}
	return false
}

func (e *RunExecutor) cpuPrimaryPostServiceHostScaleOutAndRetry(
	runID string,
	scenario *config.Scenario,
	opt *simulationv1.OptimizationConfig,
	rm *resource.Manager,
	loop *onlineCtrlLoopState,
	tick *onlineCtrlTickInput,
	cpuScratch *cpuPrimaryScaleUpScratch,
) {
	if rm == nil || tick == nil || loop == nil || cpuScratch == nil {
		return
	}
	targetUtilHigh := EffectiveOnlineTargetUtilHigh(opt)
	hostCount := rm.HostCount()
	maxHosts := tick.maxHosts
	if hostCount >= maxHosts {
		return
	}

	maxHostCPU := rm.MaxHostCPUUtilization()
	condA := cpuScratch.capacityBlockedReplicaUp
	condB := maxHostCPU > targetUtilHigh &&
		(cpuPrimaryAnyHotServiceCPU(scenario, tick.runMetrics, targetUtilHigh) || onlineAnyBrokerPressure(tick.brokerPressure))
	if !condA && !condB {
		return
	}

	reason := reasonCPUHostScaleHot
	if condA {
		reason = reasonCPUHostScalePlacement
	}

	prevConfig, _ := e.GetRunConfiguration(runID)
	if err := rm.ScaleOutHosts(hostCount + 1); err != nil {
		logger.Error("online controller cpu-primary failed to scale out hosts",
			"run_id", runID,
			"current_hosts", hostCount,
			"target_hosts", hostCount+1,
			"error", err)
		return
	}
	if rm.HostCount() <= hostCount {
		return
	}
	logger.Info("online controller cpu-primary scaled out hosts",
		"run_id", runID,
		"previous_hosts", hostCount,
		"new_hosts", rm.HostCount(),
		"max_hosts", maxHosts,
		"reason", reason)
	if currConfig, ok := e.GetRunConfiguration(runID); ok && prevConfig != nil {
		loop.stepIndex++
		e.recordOptimizationStep(runID, loop.stepIndex, tick.targetP95, tick.currentP95,
			reason, prevConfig, currConfig,
			onlineOptReplay(opt, tick, "cpu_utilization", &onlineOptimizationStepMeta{
				Action:              "host_scale_out",
				DecisionMetric:      "max_host_cpu_utilization",
				DecisionMetricValue: maxHostCPU,
			}))
	}

	if !cpuScratch.capacityBlockedReplicaUp || cpuScratch.blockedServiceID == "" {
		return
	}
	prevRetry, _ := e.GetRunConfiguration(runID)
	if err := e.UpdateServiceReplicas(runID, cpuScratch.blockedServiceID, cpuScratch.blockedTargetReplicas); err != nil {
		logger.Debug("online controller cpu-primary replica retry after host scale-out skipped",
			"run_id", runID,
			"service_id", cpuScratch.blockedServiceID,
			"target_replicas", cpuScratch.blockedTargetReplicas,
			"error", err)
		return
	}
	logger.Info("online controller cpu-primary retried replica scale-up after host add",
		"run_id", runID,
		"service_id", cpuScratch.blockedServiceID,
		"replicas", cpuScratch.blockedTargetReplicas)
	currRetry, ok := e.GetRunConfiguration(runID)
	if ok && prevRetry != nil && currRetry != nil {
		loop.stepIndex++
		svcCPU := 0.0
		if tick.runMetrics != nil && tick.runMetrics.ServiceMetrics != nil {
			if sm := tick.runMetrics.ServiceMetrics[cpuScratch.blockedServiceID]; sm != nil {
				svcCPU = sm.CPUUtilization
			}
		}
		obj := svcCPU
		e.recordOptimizationStep(runID, loop.stepIndex, tick.targetP95, tick.currentP95,
			reasonCPUReplicaScaleUp, prevRetry, currRetry,
			onlineOptReplay(opt, tick, "cpu_utilization", &onlineOptimizationStepMeta{
				Action:                 "service_scale_out",
				DecisionServiceID:      cpuScratch.blockedServiceID,
				DecisionMetric:         "service_cpu_utilization",
				DecisionMetricValue:    svcCPU,
				ObjectiveScoreOverride: &obj,
			}))
	}
}
