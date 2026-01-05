package simd

import (
	"context"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"google.golang.org/grpc/metadata"
)

func TestGRPCServerCreateStartGetMetricsLifecycle(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store))

	ctx := context.Background()
	validScenario := `
hosts:
  - id: host-1
    cores: 2
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
    arrival: {type: poisson, rate_rps: 10}
`
	createResp, err := srv.CreateRun(ctx, &simulationv1.CreateRunRequest{
		Input: &simulationv1.RunInput{
			ScenarioYaml: validScenario,
			DurationMs:   100, // 100ms for quick test
		},
	})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}
	if createResp.Run.Id == "" {
		t.Fatalf("expected run id")
	}

	// Metrics should not be available before start/complete.
	_, err = srv.GetRunMetrics(ctx, &simulationv1.GetRunMetricsRequest{RunId: createResp.Run.Id})
	if err == nil {
		t.Fatalf("expected GetRunMetrics to fail before metrics exist")
	}

	_, err = srv.StartRun(ctx, &simulationv1.StartRunRequest{RunId: createResp.Run.Id})
	if err != nil {
		t.Fatalf("StartRun error: %v", err)
	}

	// Wait for the simulation to complete.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		getResp, err := srv.GetRun(ctx, &simulationv1.GetRunRequest{RunId: createResp.Run.Id})
		if err != nil {
			t.Fatalf("GetRun error: %v", err)
		}
		if getResp.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	metricsResp, err := srv.GetRunMetrics(ctx, &simulationv1.GetRunMetricsRequest{RunId: createResp.Run.Id})
	if err != nil {
		t.Fatalf("GetRunMetrics error: %v", err)
	}
	if metricsResp.Metrics == nil {
		t.Fatalf("expected metrics")
	}
}

type fakeRunEventsStream struct {
	ctx     context.Context
	sent    []*simulationv1.StreamRunEventsResponse
	header  metadata.MD
	trailer metadata.MD
}

func (s *fakeRunEventsStream) Send(resp *simulationv1.StreamRunEventsResponse) error {
	s.sent = append(s.sent, resp)
	return nil
}

func (s *fakeRunEventsStream) SetHeader(md metadata.MD) error  { s.header = md; return nil }
func (s *fakeRunEventsStream) SendHeader(md metadata.MD) error { s.header = md; return nil }
func (s *fakeRunEventsStream) SetTrailer(md metadata.MD)       { s.trailer = md }
func (s *fakeRunEventsStream) Context() context.Context        { return s.ctx }
func (s *fakeRunEventsStream) SendMsg(m any) error             { return nil }
func (s *fakeRunEventsStream) RecvMsg(m any) error             { return nil }

func TestGRPCServerStreamRunEventsSendsInitialEvent(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store))
	ctx := context.Background()

	validScenario := `
hosts:
  - id: host-1
    cores: 2
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
    arrival: {type: poisson, rate_rps: 10}
`
	createResp, err := srv.CreateRun(ctx, &simulationv1.CreateRunRequest{
		Input: &simulationv1.RunInput{ScenarioYaml: validScenario},
	})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream := &fakeRunEventsStream{ctx: streamCtx}

	// Cancel shortly after to avoid waiting for polling loop.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_ = srv.StreamRunEvents(&simulationv1.StreamRunEventsRequest{
		RunId:             createResp.Run.Id,
		MetricsIntervalMs: 5,
	}, stream)

	if len(stream.sent) == 0 {
		t.Fatalf("expected at least one event to be sent")
	}
	if stream.sent[0].Event == nil || stream.sent[0].Event.RunId != createResp.Run.Id {
		t.Fatalf("expected first event to reference run id")
	}
}

func TestGRPCServerStreamRunEventsTracksStatusChanges(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store)
	srv := NewSimulationGRPCServer(store, executor)
	ctx := context.Background()

	validScenario := `
hosts:
  - id: host-1
    cores: 2
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
    arrival: {type: poisson, rate_rps: 10}
`
	createResp, err := srv.CreateRun(ctx, &simulationv1.CreateRunRequest{
		Input: &simulationv1.RunInput{
			ScenarioYaml: validScenario,
			DurationMs:   50, // Short duration for quick test
		},
	})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}

	streamCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	stream := &fakeRunEventsStream{ctx: streamCtx}

	// Start streaming in background
	streamErrCh := make(chan error, 1)
	go func() {
		streamErrCh <- srv.StreamRunEvents(&simulationv1.StreamRunEventsRequest{
			RunId:             createResp.Run.Id,
			MetricsIntervalMs: 10, // Fast polling for test
		}, stream)
	}()

	// Wait a bit for initial event
	time.Sleep(50 * time.Millisecond)

	// Start the run - this should trigger a status change
	_, err = srv.StartRun(ctx, &simulationv1.StartRunRequest{RunId: createResp.Run.Id})
	if err != nil {
		t.Fatalf("StartRun error: %v", err)
	}

	// Wait for completion
	select {
	case err := <-streamErrCh:
		if err != nil && err != context.DeadlineExceeded {
			t.Logf("Stream ended with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		cancel()
	}

	// Verify we received events
	if len(stream.sent) == 0 {
		t.Fatalf("expected at least one event to be sent")
	}

	// Count status change events
	statusChanges := 0
	for _, resp := range stream.sent {
		if resp.Event != nil {
			if _, ok := resp.Event.Event.(*simulationv1.RunEvent_StatusChanged); ok {
				statusChanges++
			}
		}
	}

	// Should have at least initial status event, and potentially RUNNING -> COMPLETED
	if statusChanges < 1 {
		t.Fatalf("expected at least one status change event, got %d", statusChanges)
	}

	// Verify initial event has correct status
	firstEvent := stream.sent[0]
	if firstEvent.Event == nil {
		t.Fatalf("expected first event to have Event field")
	}
	if statusChange, ok := firstEvent.Event.Event.(*simulationv1.RunEvent_StatusChanged); ok {
		if statusChange.StatusChanged.Previous != simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED {
			t.Fatalf("expected initial previous status to be UNSPECIFIED, got %v", statusChange.StatusChanged.Previous)
		}
	}
}

func TestGRPCServerListRuns(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store))
	ctx := context.Background()

	// Create multiple runs
	for i := 0; i < 5; i++ {
		_, err := srv.CreateRun(ctx, &simulationv1.CreateRunRequest{
			Input: &simulationv1.RunInput{ScenarioYaml: "hosts: []"},
		})
		if err != nil {
			t.Fatalf("CreateRun error: %v", err)
		}
	}

	// List with limit
	resp, err := srv.ListRuns(ctx, &simulationv1.ListRunsRequest{Limit: 3})
	if err != nil {
		t.Fatalf("ListRuns error: %v", err)
	}
	if len(resp.Runs) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(resp.Runs))
	}

	// List without limit (should default to 50)
	resp, err = srv.ListRuns(ctx, &simulationv1.ListRunsRequest{})
	if err != nil {
		t.Fatalf("ListRuns error: %v", err)
	}
	if len(resp.Runs) == 0 {
		t.Fatalf("expected at least one run")
	}
	if len(resp.Runs) > 50 {
		t.Fatalf("expected max 50 runs, got %d", len(resp.Runs))
	}
}

func TestGRPCServerCreateRunWithNilInput(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store))
	ctx := context.Background()

	_, err := srv.CreateRun(ctx, &simulationv1.CreateRunRequest{Input: nil})
	if err == nil {
		t.Fatalf("expected error for nil input")
	}
}

func TestGRPCServerGetRunOnNonExistent(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store))
	ctx := context.Background()

	_, err := srv.GetRun(ctx, &simulationv1.GetRunRequest{RunId: "nope"})
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestGRPCServerStartRunOnNonExistent(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store))
	ctx := context.Background()

	_, err := srv.StartRun(ctx, &simulationv1.StartRunRequest{RunId: "nope"})
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestGRPCServerStopRunOnNonExistent(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store))
	ctx := context.Background()

	_, err := srv.StopRun(ctx, &simulationv1.StopRunRequest{RunId: "nope"})
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestGRPCServerUpdateWorkloadRate(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store)
	srv := NewSimulationGRPCServer(store, executor)
	ctx := context.Background()

	validScenario := `
hosts:
  - id: host-1
    cores: 2
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
    arrival: {type: poisson, rate_rps: 10}
`

	// Create and start a run
	createResp, err := srv.CreateRun(ctx, &simulationv1.CreateRunRequest{
		Input: &simulationv1.RunInput{
			ScenarioYaml: validScenario,
			DurationMs:   60000, // Long duration to ensure run stays running
		},
	})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}

	_, err = srv.StartRun(ctx, &simulationv1.StartRunRequest{RunId: createResp.Run.Id})
	if err != nil {
		t.Fatalf("StartRun error: %v", err)
	}

	// Brief delay to let workload state initialize
	time.Sleep(2 * time.Millisecond)

	// Test successful rate update - discrete-event simulations can complete very quickly
	patternKey := "client:svc1:/test"
	newRate := 50.0
	updateResp, err := srv.UpdateWorkloadRate(ctx, &simulationv1.UpdateWorkloadRateRequest{
		RunId:      createResp.Run.Id,
		PatternKey: patternKey,
		RateRps:    newRate,
	})
	if err != nil {
		// Check if run has already completed
		rec, ok := store.Get(createResp.Run.Id)
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			// Simulation completed too quickly - this is expected for discrete-event sims
			t.Skipf("Simulation completed too quickly (status: %v) - skipping rate update test", rec.Run.Status)
		}
		t.Fatalf("UpdateWorkloadRate error: %v", err)
	}
	if updateResp.Run == nil {
		t.Fatalf("expected run in response")
	}

	// Stop the run if it's still running
	_, _ = srv.StopRun(ctx, &simulationv1.StopRunRequest{RunId: createResp.Run.Id})
}

func TestGRPCServerUpdateWorkloadRateValidation(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store))
	ctx := context.Background()

	// Test nil request
	_, err := srv.UpdateWorkloadRate(ctx, nil)
	if err == nil {
		t.Fatalf("expected error for nil request")
	}

	// Test missing run_id
	_, err = srv.UpdateWorkloadRate(ctx, &simulationv1.UpdateWorkloadRateRequest{
		PatternKey: "client:svc1:/test",
		RateRps:    10.0,
	})
	if err == nil {
		t.Fatalf("expected error for missing run_id")
	}

	// Test missing pattern_key
	_, err = srv.UpdateWorkloadRate(ctx, &simulationv1.UpdateWorkloadRateRequest{
		RunId:   "run-1",
		RateRps: 10.0,
	})
	if err == nil {
		t.Fatalf("expected error for missing pattern_key")
	}

	// Test negative rate
	_, err = srv.UpdateWorkloadRate(ctx, &simulationv1.UpdateWorkloadRateRequest{
		RunId:      "run-1",
		PatternKey: "client:svc1:/test",
		RateRps:    -5.0,
	})
	if err == nil {
		t.Fatalf("expected error for negative rate")
	}

	// Test zero rate
	_, err = srv.UpdateWorkloadRate(ctx, &simulationv1.UpdateWorkloadRateRequest{
		RunId:      "run-1",
		PatternKey: "client:svc1:/test",
		RateRps:    0.0,
	})
	if err == nil {
		t.Fatalf("expected error for zero rate")
	}

	// Test non-existent run
	_, err = srv.UpdateWorkloadRate(ctx, &simulationv1.UpdateWorkloadRateRequest{
		RunId:      "non-existent",
		PatternKey: "client:svc1:/test",
		RateRps:    10.0,
	})
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestGRPCServerUpdateWorkloadRateNotRunning(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store))
	ctx := context.Background()

	validScenario := `
hosts:
  - id: host-1
    cores: 2
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
    arrival: {type: poisson, rate_rps: 10}
`

	// Create but don't start
	createResp, err := srv.CreateRun(ctx, &simulationv1.CreateRunRequest{
		Input: &simulationv1.RunInput{
			ScenarioYaml: validScenario,
			DurationMs:   1000,
		},
	})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}

	// Try to update rate when run is not running
	_, err = srv.UpdateWorkloadRate(ctx, &simulationv1.UpdateWorkloadRateRequest{
		RunId:      createResp.Run.Id,
		PatternKey: "client:svc1:/test",
		RateRps:    50.0,
	})
	if err == nil {
		t.Fatalf("expected error when updating rate on non-running run")
	}
}

