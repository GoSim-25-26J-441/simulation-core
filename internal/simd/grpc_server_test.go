package simd

import (
	"context"
	"sync"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGRPCServerCreateStartGetMetricsLifecycle(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))

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
	deadline := time.Now().Add(1 * time.Second)
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
	mu      sync.Mutex
	sent    []*simulationv1.StreamRunEventsResponse
	header  metadata.MD
	trailer metadata.MD
}

func (s *fakeRunEventsStream) Send(resp *simulationv1.StreamRunEventsResponse) error {
	s.mu.Lock()
	s.sent = append(s.sent, resp)
	s.mu.Unlock()
	return nil
}

func (s *fakeRunEventsStream) getSent() []*simulationv1.StreamRunEventsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*simulationv1.StreamRunEventsResponse, len(s.sent))
	copy(out, s.sent)
	return out
}

func (s *fakeRunEventsStream) SetHeader(md metadata.MD) error  { s.header = md; return nil }
func (s *fakeRunEventsStream) SendHeader(md metadata.MD) error { s.header = md; return nil }
func (s *fakeRunEventsStream) SetTrailer(md metadata.MD)       { s.trailer = md }
func (s *fakeRunEventsStream) Context() context.Context        { return s.ctx }
func (s *fakeRunEventsStream) SendMsg(m any) error             { return nil }
func (s *fakeRunEventsStream) RecvMsg(m any) error             { return nil }

func TestGRPCServerStreamRunEventsEmptyRunId(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()
	stream := &fakeRunEventsStream{ctx: ctx}

	err := srv.StreamRunEvents(&simulationv1.StreamRunEventsRequest{RunId: ""}, stream)
	if err == nil {
		t.Fatalf("expected error for empty run_id")
	}
}

func TestGRPCServerStreamRunEventsRunNotFound(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	stream := &fakeRunEventsStream{ctx: ctx}

	err := srv.StreamRunEvents(&simulationv1.StreamRunEventsRequest{RunId: "nope"}, stream)
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestGRPCServerStreamRunEventsSendsInitialEvent(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
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

	sent := stream.getSent()
	if len(sent) == 0 {
		t.Fatalf("expected at least one event to be sent")
	}
	if sent[0].Event == nil || sent[0].Event.RunId != createResp.Run.Id {
		t.Fatalf("expected first event to reference run id")
	}
}

func TestGRPCServerStreamRunEventsTracksStatusChanges(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
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
	sent := stream.getSent()
	if len(sent) == 0 {
		t.Fatalf("expected at least one event to be sent")
	}

	// Count status change events
	statusChanges := 0
	for _, resp := range sent {
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
	firstEvent := sent[0]
	if firstEvent.Event == nil {
		t.Fatalf("expected first event to have Event field")
	}
	if statusChange, ok := firstEvent.Event.Event.(*simulationv1.RunEvent_StatusChanged); ok {
		if statusChange.StatusChanged.Previous != simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED {
			t.Fatalf("expected initial previous status to be UNSPECIFIED, got %v", statusChange.StatusChanged.Previous)
		}
	}
}

func TestGRPCServerStreamRunEventsOptimizationProgress(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
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
			DurationMs:   100,
			Optimization: &simulationv1.OptimizationConfig{
				Objective:     "p95_latency_ms",
				MaxIterations: 5,
				StepSize:      1.0,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}

	streamCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	stream := &fakeRunEventsStream{ctx: streamCtx}

	// Set status to running and optimization progress before streaming
	_, _ = store.SetStatus(createResp.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, "")
	store.SetOptimizationProgress(createResp.Run.Id, 1, 12.5)

	_ = srv.StreamRunEvents(&simulationv1.StreamRunEventsRequest{
		RunId:             createResp.Run.Id,
		MetricsIntervalMs: 10,
	}, stream)

	sent := stream.getSent()
	var optProgress *simulationv1.OptimizationProgress
	for _, resp := range sent {
		if resp.Event != nil {
			if prog, ok := resp.Event.Event.(*simulationv1.RunEvent_OptimizationProgress); ok {
				optProgress = prog.OptimizationProgress
				break
			}
		}
	}
	if optProgress == nil {
		t.Fatalf("expected OptimizationProgress event in stream, got %d events", len(sent))
	}
	if optProgress.Iteration != 1 {
		t.Errorf("expected iteration 1, got %d", optProgress.Iteration)
	}
	if optProgress.BestScore != 12.5 {
		t.Errorf("expected best_score 12.5, got %f", optProgress.BestScore)
	}
	if optProgress.Objective != "p95_latency" {
		t.Errorf("expected objective p95_latency, got %q", optProgress.Objective)
	}
	if optProgress.Unit != "ms" {
		t.Errorf("expected unit ms, got %q", optProgress.Unit)
	}
}

func TestGRPCServerListRuns(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
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

func TestGRPCServerCreateRunWithNilRequest(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.CreateRun(ctx, nil)
	if err == nil {
		t.Fatalf("expected error for nil request")
	}
}

func TestGRPCServerStartRunWithEmptyRunId(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.StartRun(ctx, &simulationv1.StartRunRequest{RunId: ""})
	if err == nil {
		t.Fatalf("expected error for empty run_id")
	}
}

func TestGRPCServerGetRunWithEmptyRunId(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.GetRun(ctx, &simulationv1.GetRunRequest{RunId: ""})
	if err == nil {
		t.Fatalf("expected error for empty run_id")
	}
}

func TestGRPCServerGetRunMetricsWithEmptyRunId(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.GetRunMetrics(ctx, &simulationv1.GetRunMetricsRequest{RunId: ""})
	if err == nil {
		t.Fatalf("expected error for empty run_id")
	}
}

func TestGRPCServerCreateRunWithNilInput(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.CreateRun(ctx, &simulationv1.CreateRunRequest{Input: nil})
	if err == nil {
		t.Fatalf("expected error for nil input")
	}
}

func TestGRPCServerGetRunOnNonExistent(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.GetRun(ctx, &simulationv1.GetRunRequest{RunId: "nope"})
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestGRPCServerStartRunOnNonExistent(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.StartRun(ctx, &simulationv1.StartRunRequest{RunId: "nope"})
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestGRPCServerStopRunWithEmptyRunId(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.StopRun(ctx, &simulationv1.StopRunRequest{RunId: ""})
	if err == nil {
		t.Fatalf("expected error for empty run_id")
	}
}

func TestGRPCServerStopRunOnNonExistent(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.StopRun(ctx, &simulationv1.StopRunRequest{RunId: "nope"})
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestGRPCServerUpdateWorkloadRate(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
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

	// Use real-time mode so the simulation actually runs for ~300ms; discrete-event mode
	// completes in microseconds, making UpdateWorkloadRate impossible to test reliably.
	createResp, err := srv.CreateRun(ctx, &simulationv1.CreateRunRequest{
		Input: &simulationv1.RunInput{
			ScenarioYaml: validScenario,
			DurationMs:   300,
			RealTimeMode: true,
		},
	})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}

	_, err = srv.StartRun(ctx, &simulationv1.StartRunRequest{RunId: createResp.Run.Id})
	if err != nil {
		t.Fatalf("StartRun error: %v", err)
	}

	// Brief delay for workload state to initialize, then update rate
	time.Sleep(50 * time.Millisecond)
	patternKey := "client:svc1:/test"
	newRate := 50.0
	updateResp, err := srv.UpdateWorkloadRate(ctx, &simulationv1.UpdateWorkloadRateRequest{
		RunId:      createResp.Run.Id,
		PatternKey: patternKey,
		RateRps:    newRate,
	})
	if err != nil {
		rec, ok := store.Get(createResp.Run.Id)
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			t.Skipf("Simulation completed too quickly (status: %v) - skipping rate update test", rec.Run.Status)
		}
		t.Fatalf("UpdateWorkloadRate error: %v", err)
	}
	if updateResp.Run == nil {
		t.Fatalf("expected run in response")
	}

	// Stop the run (real-time sim would otherwise run ~300ms)
	_, _ = srv.StopRun(ctx, &simulationv1.StopRunRequest{RunId: createResp.Run.Id})
}

func TestGRPCServerUpdateWorkloadRateValidation(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
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
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
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

func TestGRPCServerUpdateRunConfigurationVerticalScaling(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
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
			DurationMs:   1000,
		},
	})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}

	if _, err := srv.StartRun(ctx, &simulationv1.StartRunRequest{RunId: createResp.Run.Id}); err != nil {
		t.Fatalf("StartRun error: %v", err)
	}

	// Wait briefly for initialization but ensure run is still RUNNING
	time.Sleep(10 * time.Millisecond)
	rec, ok := store.Get(createResp.Run.Id)
	if !ok {
		t.Fatal("run not found after start")
	}
	if rec.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run is not RUNNING (status=%v), skipping vertical scaling test", rec.Run.Status)
	}

	_, err = srv.UpdateRunConfiguration(ctx, &simulationv1.UpdateRunConfigurationRequest{
		RunId: createResp.Run.Id,
		Services: []*simulationv1.ServiceReplicasUpdate{
			{
				ServiceId: "svc1",
				Replicas:  2,
				CpuCores:  4.0,
				MemoryMb:  2048.0,
			},
		},
	})
	if err != nil {
		t.Fatalf("UpdateRunConfiguration error: %v", err)
	}

	// Verify via GetRunConfiguration
	cfgResp, err := srv.GetRunConfiguration(ctx, &simulationv1.GetRunConfigurationRequest{
		RunId: createResp.Run.Id,
	})
	if err != nil {
		t.Fatalf("GetRunConfiguration error: %v", err)
	}
	if cfgResp.Configuration == nil || len(cfgResp.Configuration.Services) == 0 {
		t.Fatalf("expected configuration with at least one service")
	}
	var svcCfg *simulationv1.ServiceConfigEntry
	for _, sCfg := range cfgResp.Configuration.Services {
		if sCfg.ServiceId == "svc1" {
			svcCfg = sCfg
			break
		}
	}
	if svcCfg == nil {
		t.Fatalf("expected svc1 in configuration")
	}
	if svcCfg.CpuCores != 4.0 {
		t.Fatalf("expected cpu_cores=4.0, got %f", svcCfg.CpuCores)
	}
	if svcCfg.MemoryMb != 2048.0 {
		t.Fatalf("expected memory_mb=2048.0, got %f", svcCfg.MemoryMb)
	}
}

func TestGRPCServerUpdateRunConfiguration(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
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
			DurationMs:   2000,
			RealTimeMode: true,
		},
	})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}

	_, err = srv.StartRun(ctx, &simulationv1.StartRunRequest{RunId: createResp.Run.Id})
	if err != nil {
		t.Fatalf("StartRun error: %v", err)
	}

	time.Sleep(80 * time.Millisecond)

	updateResp, err := srv.UpdateRunConfiguration(ctx, &simulationv1.UpdateRunConfigurationRequest{
		RunId: createResp.Run.Id,
		Services: []*simulationv1.ServiceReplicasUpdate{
			{ServiceId: "svc1", Replicas: 2},
		},
	})
	if err != nil {
		rec, ok := store.Get(createResp.Run.Id)
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			t.Skipf("Simulation completed too quickly - skipping UpdateRunConfiguration test")
		}
		t.Fatalf("UpdateRunConfiguration error: %v", err)
	}
	if updateResp.Run == nil {
		t.Fatalf("expected run in response")
	}

	_, _ = srv.StopRun(ctx, &simulationv1.StopRunRequest{RunId: createResp.Run.Id})
}

func TestGRPCServerUpdateRunConfigurationValidation(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.UpdateRunConfiguration(ctx, nil)
	if err == nil {
		t.Fatalf("expected error for nil request")
	}

	_, err = srv.UpdateRunConfiguration(ctx, &simulationv1.UpdateRunConfigurationRequest{
		RunId: "",
		Services: []*simulationv1.ServiceReplicasUpdate{
			{ServiceId: "svc1", Replicas: 2},
		},
	})
	if err == nil {
		t.Fatalf("expected error for empty run_id")
	}

	_, err = srv.UpdateRunConfiguration(ctx, &simulationv1.UpdateRunConfigurationRequest{
		RunId:    "run-1",
		Services: []*simulationv1.ServiceReplicasUpdate{},
	})
	if err == nil {
		t.Fatalf("expected error for empty services")
	}

	_, err = srv.UpdateRunConfiguration(ctx, &simulationv1.UpdateRunConfigurationRequest{
		RunId: "non-existent",
		Services: []*simulationv1.ServiceReplicasUpdate{
			{ServiceId: "svc1", Replicas: 2},
		},
	})
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestGRPCServerGetRunConfiguration(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
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
			DurationMs:   2000,
			RealTimeMode: true,
		},
	})
	if err != nil {
		t.Fatalf("CreateRun error: %v", err)
	}

	_, err = srv.StartRun(ctx, &simulationv1.StartRunRequest{RunId: createResp.Run.Id})
	if err != nil {
		t.Fatalf("StartRun error: %v", err)
	}

	time.Sleep(80 * time.Millisecond)

	getResp, err := srv.GetRunConfiguration(ctx, &simulationv1.GetRunConfigurationRequest{
		RunId: createResp.Run.Id,
	})
	if err != nil {
		rec, ok := store.Get(createResp.Run.Id)
		if ok && rec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			t.Skipf("Simulation completed too quickly - skipping GetRunConfiguration test")
		}
		t.Fatalf("GetRunConfiguration error: %v", err)
	}
	if getResp.Configuration == nil {
		t.Fatalf("expected configuration in response")
	}
	if len(getResp.Configuration.Services) == 0 {
		t.Error("expected at least one service in configuration")
	}

	_, _ = srv.StopRun(ctx, &simulationv1.StopRunRequest{RunId: createResp.Run.Id})
}

func TestGRPCServerGetRunConfigurationValidation(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.GetRunConfiguration(ctx, nil)
	if err == nil {
		t.Fatalf("expected error for nil request")
	}

	_, err = srv.GetRunConfiguration(ctx, &simulationv1.GetRunConfigurationRequest{RunId: ""})
	if err == nil {
		t.Fatalf("expected error for empty run_id")
	}

	_, err = srv.GetRunConfiguration(ctx, &simulationv1.GetRunConfigurationRequest{RunId: "non-existent"})
	if err == nil {
		t.Fatalf("expected error for non-existent run")
	}
}

func TestGRPCServerRenewOnlineLease(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewSimulationGRPCServer(store, exec)
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
	rec, err := store.Create("grpc-lease-run", &simulationv1.RunInput{
		ScenarioYaml: validScenario,
		DurationMs:   100,
		Optimization: &simulationv1.OptimizationConfig{
			Online:             true,
			TargetP95LatencyMs: 50,
			LeaseTtlMs:         60_000,
		},
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	resp, err := srv.RenewOnlineLease(ctx, &simulationv1.RenewOnlineLeaseRequest{RunId: rec.Run.Id})
	if err != nil {
		t.Fatalf("RenewOnlineLease: %v", err)
	}
	if resp.GetRun() == nil || resp.GetRun().GetId() != rec.Run.Id {
		t.Fatalf("expected run id in response, got %#v", resp.GetRun())
	}
}

func TestGRPCServerRenewOnlineLeaseNotFound(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
	ctx := context.Background()

	_, err := srv.RenewOnlineLease(ctx, &simulationv1.RenewOnlineLeaseRequest{RunId: "missing-run"})
	if err == nil {
		t.Fatal("expected error")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestGRPCServerRenewOnlineLeaseLeaseNotConfigured(t *testing.T) {
	store := NewRunStore()
	srv := NewSimulationGRPCServer(store, NewRunExecutor(store, nil))
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
	rec, err := store.Create("grpc-lease-not-configured", &simulationv1.RunInput{
		ScenarioYaml: validScenario,
		DurationMs:   100,
		Optimization: &simulationv1.OptimizationConfig{
			Online:               true,
			TargetP95LatencyMs:   50,
			AllowUnboundedOnline: true,
			MaxOnlineDurationMs:  0,
		},
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	_, err = srv.RenewOnlineLease(ctx, &simulationv1.RenewOnlineLeaseRequest{RunId: rec.Run.Id})
	if err == nil {
		t.Fatal("expected error")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", err)
	}
}
