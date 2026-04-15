package simd

import (
	"fmt"
	"math"
	"sort"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// UpdateServiceReplicas updates the number of replicas for a service in a running simulation
func (e *RunExecutor) UpdateServiceReplicas(runID string, serviceID string, replicas int) error {
	if runID == "" {
		return ErrRunIDMissing
	}
	if serviceID == "" {
		return fmt.Errorf("service_id is required")
	}

	e.mu.Lock()
	rm, ok := e.resourceManagers[runID]
	ws, wsOk := e.workloadStates[runID]
	e.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	simTime := time.Now()
	if wsOk {
		if eng := ws.Engine(); eng != nil {
			simTime = eng.GetSimTime()
		}
	}
	return rm.ScaleServiceWithOptions(serviceID, replicas, resource.ScaleServiceOptions{SimTime: simTime})
}

// UpdateServiceResources updates per-instance CPU cores and memory (MB) for a service
// in a running simulation. Passing 0 for a field leaves it unchanged.
func (e *RunExecutor) UpdateServiceResources(runID string, serviceID string, cpuCores, memoryMB float64) error {
	if runID == "" {
		return ErrRunIDMissing
	}
	if serviceID == "" {
		return fmt.Errorf("service_id is required")
	}
	if cpuCores < 0 || memoryMB < 0 {
		return fmt.Errorf("cpu_cores and memory_mb must be non-negative")
	}
	if cpuCores == 0 && memoryMB == 0 {
		return nil
	}

	e.mu.Lock()
	rm, ok := e.resourceManagers[runID]
	e.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	return rm.UpdateServiceResources(serviceID, cpuCores, memoryMB)
}

// UpdateServiceResourcesWithHeadroom updates per-instance CPU/memory like UpdateServiceResources
// but supplies memory headroom (MB) for safe memory downsize validation.
func (e *RunExecutor) UpdateServiceResourcesWithHeadroom(runID string, serviceID string, cpuCores, memoryMB, memoryHeadroomMB float64) error {
	if runID == "" {
		return ErrRunIDMissing
	}
	if serviceID == "" {
		return fmt.Errorf("service_id is required")
	}
	if cpuCores < 0 || memoryMB < 0 {
		return fmt.Errorf("cpu_cores and memory_mb must be non-negative")
	}
	if cpuCores == 0 && memoryMB == 0 {
		return nil
	}

	e.mu.Lock()
	rm, ok := e.resourceManagers[runID]
	e.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	return rm.UpdateServiceResourcesWithHeadroom(serviceID, cpuCores, memoryMB, memoryHeadroomMB)
}

// UpdatePolicies updates policies (e.g. autoscaling) for a running simulation
func (e *RunExecutor) UpdatePolicies(runID string, policies *config.Policies) error {
	if runID == "" {
		return ErrRunIDMissing
	}

	e.mu.Lock()
	pm, ok := e.policyManagers[runID]
	e.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	if policies != nil && policies.Autoscaling != nil {
		pm.UpdateAutoscaling(policies.Autoscaling)
	}
	return nil
}

// UpdateWorkloadRate updates the rate for a specific workload pattern in a running simulation
func (e *RunExecutor) UpdateWorkloadRate(runID string, patternKey string, newRateRPS float64) error {
	if runID == "" {
		return ErrRunIDMissing
	}

	if newRateRPS <= 0 {
		return fmt.Errorf("rate must be positive, got: %f", newRateRPS)
	}

	e.mu.Lock()
	workloadState, ok := e.workloadStates[runID]
	e.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	return workloadState.UpdateRate(patternKey, newRateRPS)
}

// UpdateWorkloadPattern updates an entire workload pattern in a running simulation
func (e *RunExecutor) UpdateWorkloadPattern(runID string, patternKey string, pattern config.WorkloadPattern) error {
	if runID == "" {
		return ErrRunIDMissing
	}

	e.mu.Lock()
	workloadState, ok := e.workloadStates[runID]
	e.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	return workloadState.UpdatePattern(patternKey, pattern)
}

// GetWorkloadPattern returns a workload pattern for a running simulation
func (e *RunExecutor) GetWorkloadPattern(runID string, patternKey string) (*WorkloadPatternState, bool) {
	if runID == "" {
		return nil, false
	}

	e.mu.Lock()
	workloadState, ok := e.workloadStates[runID]
	e.mu.Unlock()

	if !ok {
		return nil, false
	}

	return workloadState.GetPattern(patternKey)
}

// GetScenarioSnapshot returns the parsed scenario for an active run when available.
func (e *RunExecutor) GetScenarioSnapshot(runID string) (*config.Scenario, bool) {
	if runID == "" {
		return nil, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.runScenarios[runID]
	if !ok || s == nil {
		return nil, false
	}
	return s, true
}

// GetRunConfiguration returns the current effective configuration for a running run (replicas per service, workload rates).
func (e *RunExecutor) GetRunConfiguration(runID string) (*simulationv1.RunConfiguration, bool) {
	if runID == "" {
		return nil, false
	}

	e.mu.Lock()
	rm, rmOk := e.resourceManagers[runID]
	ws, wsOk := e.workloadStates[runID]
	e.mu.Unlock()

	if !rmOk || !wsOk {
		return nil, false
	}

	cfg := &simulationv1.RunConfiguration{}
	for _, svcID := range rm.ListServiceIDs() {
		n := rm.ActiveReplicas(svcID)
		var replicas int32
		switch {
		case n < 0:
			replicas = 0
		case n > math.MaxInt32:
			replicas = math.MaxInt32
		default:
			replicas = int32(n)
		}
		// Derive per-instance CPU/memory from a routable instance when possible.
		var cpuCores, memoryMB float64
		var foundRoutable bool
		instances := rm.GetInstancesForService(svcID)
		for _, inst := range instances {
			if inst.IsRoutable() {
				cpuCores = inst.CPUCores()
				memoryMB = inst.MemoryMB()
				foundRoutable = true
				break
			}
		}
		if !foundRoutable && len(instances) > 0 {
			cpuCores = instances[0].CPUCores()
			memoryMB = instances[0].MemoryMB()
		}
		cfg.Services = append(cfg.Services, &simulationv1.ServiceConfigEntry{
			ServiceId: svcID,
			Replicas:  replicas,
			CpuCores:  cpuCores,
			MemoryMb:  memoryMB,
		})
	}

	// Populate host allocations (CPU cores and memory in GB) from the resource manager.
	for _, hostID := range rm.HostIDs() {
		if host, ok := rm.GetHost(hostID); ok {
			cfg.Hosts = append(cfg.Hosts, &simulationv1.HostConfigEntry{
				HostId:   hostID,
				CpuCores: clampIntToInt32(host.CPUCores()),
				MemoryGb: clampIntToInt32(host.MemoryGB()),
			})
		}
	}
	patterns := ws.GetAllPatterns()
	keys := make([]string, 0, len(patterns))
	for key := range patterns {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		state := patterns[key]
		if state != nil && state.Pattern.Arrival.RateRPS > 0 {
			cfg.Workload = append(cfg.Workload, &simulationv1.WorkloadPatternEntry{
				PatternKey: key,
				RateRps:    state.Pattern.Arrival.RateRPS,
			})
		}
	}

	simTime := time.Now()
	if eng := ws.Engine(); eng != nil {
		simTime = eng.GetSimTime()
	}
	for _, p := range rm.GetInstancePlacements(simTime) {
		cfg.Placements = append(cfg.Placements, &simulationv1.InstancePlacementEntry{
			InstanceId:        p.InstanceID,
			ServiceId:         p.ServiceID,
			HostId:            p.HostID,
			Lifecycle:         p.Lifecycle,
			CpuCores:          p.CPUCores,
			MemoryMb:          p.MemoryMB,
			CpuUtilization:    p.CPUUtilization,
			MemoryUtilization: p.MemoryUtilization,
			ActiveRequests:    p.ActiveRequests,
			QueueLength:       p.QueueLength,
		})
	}

	return cfg, true
}

// clampIntToInt32 safely converts an int to int32, clamping to the int32 range.
func clampIntToInt32(n int) int32 {
	const max = 1<<31 - 1
	const min = -1 << 31
	if n > max {
		return max
	}
	if n < min {
		return min
	}
	return int32(n)
}

func instanceIDsByServiceFromRM(rm *resource.Manager) map[string][]string {
	if rm == nil {
		return nil
	}
	out := make(map[string][]string)
	for _, svcID := range rm.ListServiceIDs() {
		names := make([]string, 0)
		for _, inst := range rm.GetInstancesForService(svcID) {
			names = append(names, inst.ID())
		}
		sort.Strings(names)
		out[svcID] = names
	}
	return out
}

// runMetricsOptsForRun builds conversion options so service CPU/memory/concurrent rollups include idle replicas (0 when no samples).
func (e *RunExecutor) runMetricsOptsForRun(runID string) *metrics.RunMetricsOptions {
	e.mu.Lock()
	rm, ok := e.resourceManagers[runID]
	e.mu.Unlock()
	if !ok || rm == nil {
		return nil
	}
	snapshotAt := rm.LastSimTime()
	if snapshotAt.IsZero() {
		snapshotAt = time.Now()
	}
	bySvc := instanceIDsByServiceFromRM(rm)
	queueSnaps := rm.QueueBrokerHealthSnapshots(snapshotAt)
	topicSnaps := rm.TopicBrokerHealthSnapshots(snapshotAt)
	if len(bySvc) == 0 && len(queueSnaps) == 0 && len(topicSnaps) == 0 {
		return nil
	}
	return &metrics.RunMetricsOptions{
		InstanceIDsByService: bySvc,
		QueueBrokerSnapshots: queueSnaps,
		TopicBrokerSnapshots: topicSnaps,
	}
}

// hostIDsForRun returns the resource manager's host IDs for a run (sorted), or nil when
// there is no manager for the run. Used for live SSE host_metrics to match RM inventory.
func (e *RunExecutor) hostIDsForRun(runID string) []string {
	e.mu.Lock()
	rm, ok := e.resourceManagers[runID]
	e.mu.Unlock()
	if !ok || rm == nil {
		return nil
	}
	return rm.HostIDs()
}
