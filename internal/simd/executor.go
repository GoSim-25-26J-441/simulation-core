package simd

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/logger"
)

// RunExecutor manages asynchronous run execution and per-run cancellation.
//
// Milestone 2 starts by centralizing run lifecycle and cancellation here.
// The actual simulation logic will be plugged in by later tasks (engine handlers).
type RunExecutor struct {
	store *RunStore

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

var (
	ErrRunNotFound  = errors.New("run not found")
	ErrRunTerminal  = errors.New("run is terminal")
	ErrRunIDMissing = errors.New("run_id is required")
)

func NewRunExecutor(store *RunStore) *RunExecutor {
	return &RunExecutor{
		store:   store,
		cancels: make(map[string]context.CancelFunc),
	}
}

// Start begins executing a run asynchronously.
// Returns the updated run state (RUNNING) or an error.
func (e *RunExecutor) Start(runID string) (*RunRecord, error) {
	if runID == "" {
		return nil, ErrRunIDMissing
	}

	rec, ok := e.store.Get(runID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}

	switch rec.Run.Status {
	case simulationv1.RunStatus_RUN_STATUS_RUNNING:
		return rec, nil
	case simulationv1.RunStatus_RUN_STATUS_COMPLETED,
		simulationv1.RunStatus_RUN_STATUS_FAILED,
		simulationv1.RunStatus_RUN_STATUS_CANCELLED:
		return nil, fmt.Errorf("%w: %s", ErrRunTerminal, runID)
	}

	updated, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_RUNNING, "")
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.mu.Lock()
	// Replace any existing cancel func (shouldn't happen for non-running, but safe).
	if old, exists := e.cancels[runID]; exists {
		old()
	}
	e.cancels[runID] = cancel
	e.mu.Unlock()

	go e.runSkeleton(ctx, runID)
	return updated, nil
}

// Stop requests cancellation for a running run and marks it cancelled.
func (e *RunExecutor) Stop(runID string) (*RunRecord, error) {
	if runID == "" {
		return nil, ErrRunIDMissing
	}

	e.mu.Lock()
	cancel, ok := e.cancels[runID]
	e.mu.Unlock()

	if ok {
		cancel()
	}

	updated, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_CANCELLED, "")
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (e *RunExecutor) cleanup(runID string) {
	e.mu.Lock()
	if cancel, ok := e.cancels[runID]; ok {
		// Ensure cancel is called and remove.
		cancel()
		delete(e.cancels, runID)
	}
	e.mu.Unlock()
}

func (e *RunExecutor) runSkeleton(ctx context.Context, runID string) {
	defer e.cleanup(runID)

	// Milestone 2 placeholder: wait briefly, unless cancelled.
	select {
	case <-ctx.Done():
		logger.Info("run cancelled (executor)", "run_id", runID)
		return
	case <-time.After(10 * time.Millisecond):
	}

	if err := e.store.SetMetrics(runID, &simulationv1.RunMetrics{
		TotalRequests:      0,
		SuccessfulRequests: 0,
		FailedRequests:     0,
		ThroughputRps:      0,
	}); err != nil {
		logger.Error("failed to set metrics", "run_id", runID, "error", err)
	}

	// Only mark completed if we haven't been cancelled already.
	rec, ok := e.store.Get(runID)
	if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_RUNNING {
		if _, err := e.store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_COMPLETED, ""); err != nil {
			logger.Error("failed to set status", "run_id", runID, "error", err)
		}
		logger.Info("run completed (executor skeleton)", "run_id", runID)
	}
}
