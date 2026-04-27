package simd

// Integration tests for online_completion_reason values. duration_limit, converged, and
// heartbeat_expired are asserted end-to-end. controller_steps_limit is covered in
// executor_test.go (TestRunExecutorOnlineControllerMaxControllerStepsSignalsReason).

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

const testOnlineScenarioYAML = `
hosts:
  - id: host-1
    cores: 4
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 5}
`

// TestConcurrentOnlineStartNeverExceedsCap verifies MaxConcurrentOnlineRuns is enforced
// atomically: many concurrent Starts cannot leave more than N runs RUNNING.
func TestConcurrentOnlineStartNeverExceedsCap(t *testing.T) {
	store := NewRunStore()
	lim := DefaultOnlineRunLimits()
	lim.MaxConcurrentOnlineRuns = 2
	store.SetOnlineLimits(lim)
	exec := NewRunExecutor(store, nil)

	const n = 5
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("conc-online-%d-%d", i, time.Now().UnixNano())
		_, err := store.Create(ids[i], &simulationv1.RunInput{
			ScenarioYaml: testOnlineScenarioYAML,
			DurationMs:   0,
			Optimization: &simulationv1.OptimizationConfig{
				Online:               true,
				TargetP95LatencyMs:   50,
				ControlIntervalMs:    100,
				AllowUnboundedOnline: true,
				MaxNoopIntervals:     -1,
			},
		})
		if err != nil {
			t.Fatalf("Create %s: %v", ids[i], err)
		}
	}

	var ok, rej atomic.Int32
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(runID string) {
			defer wg.Done()
			_, err := exec.Start(runID)
			if err != nil {
				if errors.Is(err, ErrOnlineRunConcurrencyLimit) {
					rej.Add(1)
				} else {
					t.Errorf("Start %s: %v", runID, err)
				}
				return
			}
			ok.Add(1)
		}(id)
	}
	wg.Wait()

	if ok.Load() != 2 || rej.Load() != 3 {
		t.Fatalf("expected exactly 2 starts and 3 concurrency rejects, got ok=%d rej=%d", ok.Load(), rej.Load())
	}
	if store.CountRunningOnline() != 2 {
		t.Fatalf("expected 2 RUNNING online runs, got %d", store.CountRunningOnline())
	}
}

func waitUntilOnlineTerminal(t *testing.T, store *RunStore, runID string, deadline time.Duration) *RunRecord {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		rec, ok := store.Get(runID)
		if !ok {
			t.Fatal("run missing")
		}
		switch rec.Run.Status {
		case simulationv1.RunStatus_RUN_STATUS_COMPLETED, simulationv1.RunStatus_RUN_STATUS_FAILED,
			simulationv1.RunStatus_RUN_STATUS_STOPPED:
			return rec
		}
		time.Sleep(15 * time.Millisecond)
	}
	rec, _ := store.Get(runID)
	t.Fatalf("run %s did not finish: status=%v reason=%q", runID, rec.Run.Status, rec.Run.OnlineCompletionReason)
	return nil
}

func TestOnlineRunCompletionDurationLimit(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "online-reason-duration"
	_, err := store.Create(runID, &simulationv1.RunInput{
		ScenarioYaml: testOnlineScenarioYAML,
		DurationMs:   0,
		Optimization: &simulationv1.OptimizationConfig{
			Online:              true,
			TargetP95LatencyMs:  50,
			ControlIntervalMs:   200,
			MaxOnlineDurationMs: 120,
			MaxNoopIntervals:    -1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Start(runID); err != nil {
		t.Fatal(err)
	}
	rec := waitUntilOnlineTerminal(t, store, runID, 5*time.Second)
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("status=%v err=%q", rec.Run.Status, rec.Run.Error)
	}
	if got := rec.Run.OnlineCompletionReason; got != OnlineCompletionDurationLimit {
		t.Fatalf("online_completion_reason=%q want %q", got, OnlineCompletionDurationLimit)
	}
}

func TestOnlineRunCompletionHeartbeatExpired(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "online-reason-lease"
	_, err := store.Create(runID, &simulationv1.RunInput{
		ScenarioYaml: testOnlineScenarioYAML,
		DurationMs:   0,
		Optimization: &simulationv1.OptimizationConfig{
			Online:               true,
			TargetP95LatencyMs:   50,
			ControlIntervalMs:    100,
			AllowUnboundedOnline: true,
			MaxNoopIntervals:     -1,
			LeaseTtlMs:           300,
			MaxOnlineDurationMs:  600000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Start(runID); err != nil {
		t.Fatal(err)
	}
	rec := waitUntilOnlineTerminal(t, store, runID, 15*time.Second)
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("status=%v err=%q", rec.Run.Status, rec.Run.Error)
	}
	if got := rec.Run.OnlineCompletionReason; got != OnlineCompletionHeartbeatExpired {
		t.Fatalf("online_completion_reason=%q want %q", got, OnlineCompletionHeartbeatExpired)
	}
}

func TestOnlineRunCompletionConverged(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "online-reason-converged"
	_, err := store.Create(runID, &simulationv1.RunInput{
		ScenarioYaml: testOnlineScenarioYAML,
		DurationMs:   0,
		Optimization: &simulationv1.OptimizationConfig{
			Online:               true,
			TargetP95LatencyMs:   50,
			ControlIntervalMs:    20,
			AllowUnboundedOnline: true,
			MaxNoopIntervals:     4,
			MaxOnlineDurationMs:  600000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.Start(runID); err != nil {
		t.Fatal(err)
	}
	rec := waitUntilOnlineTerminal(t, store, runID, 30*time.Second)
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("status=%v err=%q", rec.Run.Status, rec.Run.Error)
	}
	if got := rec.Run.OnlineCompletionReason; got != OnlineCompletionConverged {
		t.Fatalf("online_completion_reason=%q want %q", got, OnlineCompletionConverged)
	}
}
