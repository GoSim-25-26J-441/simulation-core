package simd

import (
	"fmt"
	"math"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
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
	e.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	return rm.ScaleService(serviceID, replicas)
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
		// Derive per-instance CPU/memory from one of the active instances.
		var cpuCores, memoryMB float64
		instances := rm.GetInstancesForService(svcID)
		if len(instances) > 0 {
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
	patterns := ws.GetAllPatterns()
	for key, state := range patterns {
		if state != nil && state.Pattern.Arrival.RateRPS > 0 {
			cfg.Workload = append(cfg.Workload, &simulationv1.WorkloadPatternEntry{
				PatternKey: key,
				RateRps:    state.Pattern.Arrival.RateRPS,
			})
		}
	}
	return cfg, true
}
