package simd

import (
	"fmt"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// UpdateWorkloadRate updates the rate for a specific workload pattern in a running simulation
func (e *RunExecutor) UpdateWorkloadRate(runID string, patternKey string, newRateRPS float64) error {
	if runID == "" {
		return ErrRunIDMissing
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

