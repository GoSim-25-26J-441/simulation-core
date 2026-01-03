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
	srv := NewSimulationGRPCServer(store)

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
	srv := NewSimulationGRPCServer(store)
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
