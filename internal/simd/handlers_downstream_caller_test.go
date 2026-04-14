package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func sumMetricSeries(c *metrics.Collector, name string) float64 {
	s := c.GetSummary()
	if s.Metrics == nil {
		return 0
	}
	var sum float64
	for _, v := range s.Metrics[name] {
		sum += v
	}
	return sum
}

func meanRootLatency(c *metrics.Collector) float64 {
	s := c.GetSummary()
	vals := s.Metrics[metrics.MetricRootRequestLatency]
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func TestDownstreamFractionCPUSyncIncreasesRootLatencyVsZero(t *testing.T) {
	root0 := runTwoTierSyncScenario(t, 0)
	rootHalf := runTwoTierSyncScenario(t, 0.5)
	if rootHalf <= root0+4 {
		t.Fatalf("expected materially higher root latency with fraction 0.5, got root0=%v rootHalf=%v", root0, rootHalf)
	}
}

func runTwoTierSyncScenario(t *testing.T, fraction float64) float64 {
	t.Helper()
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 8}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{
						Path: "/ingress", MeanCPUMs: 10, CPUSigmaMs: 0,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
						Downstream: []config.DownstreamCall{{
							To:                    "svc2:/api",
							Mode:                  "sync",
							CallLatencyMs:         config.LatencySpec{Mean: 10, Sigma: 0},
							DownstreamFractionCPU: fraction,
						}},
					},
				},
			},
			{
				ID: "svc2", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{
						Path: "/api", MeanCPUMs: 5, CPUSigmaMs: 0,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("init: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 42)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	RegisterHandlers(eng, state)
	inst, err := state.rm.SelectInstanceForService("svc1")
	if err != nil {
		t.Fatalf("instance: %v", err)
	}
	req := &models.Request{
		ID: "req-1", TraceID: "t1", ServiceName: "svc1", Endpoint: "/ingress",
		Status: models.RequestStatusPending, ArrivalTime: eng.GetSimTime(),
		Metadata: map[string]interface{}{"instance_id": inst.ID()},
	}
	eng.GetRunManager().AddRequest(req)
	eng.ScheduleAt(engine.EventTypeRequestStart, eng.GetSimTime(), req, "svc1", map[string]interface{}{
		"endpoint_path": "/ingress",
		"instance_id":   inst.ID(),
	})
	if err := eng.Run(2 * time.Second); err != nil {
		t.Fatalf("run: %v", err)
	}
	collector.Stop()
	return meanRootLatency(collector)
}

func TestDownstreamFractionCPUAsyncParentHopIgnoresChildDuration(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 8}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{
						Path: "/ingress", MeanCPUMs: 10, CPUSigmaMs: 0,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
						Downstream: []config.DownstreamCall{{
							To:                    "svc2:/api",
							Mode:                  "async",
							CallLatencyMs:         config.LatencySpec{Mean: 10, Sigma: 0},
							DownstreamFractionCPU: 0.5,
						}},
					},
				},
			},
			{
				ID: "svc2", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{
						Path: "/api", MeanCPUMs: 2000, CPUSigmaMs: 0,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("init: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 99)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	RegisterHandlers(eng, state)
	inst, err := state.rm.SelectInstanceForService("svc1")
	if err != nil {
		t.Fatalf("instance: %v", err)
	}
	req := &models.Request{
		ID: "req-1", TraceID: "t1", ServiceName: "svc1", Endpoint: "/ingress",
		Status: models.RequestStatusPending, ArrivalTime: eng.GetSimTime(),
		Metadata: map[string]interface{}{"instance_id": inst.ID()},
	}
	eng.GetRunManager().AddRequest(req)
	eng.ScheduleAt(engine.EventTypeRequestStart, eng.GetSimTime(), req, "svc1", map[string]interface{}{
		"endpoint_path": "/ingress",
		"instance_id":   inst.ID(),
	})
	if err := eng.Run(3 * time.Second); err != nil {
		t.Fatalf("run: %v", err)
	}
	collector.Stop()
	agg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricServiceRequestLatency, map[string]string{"service": "svc1"})
	if agg == nil {
		t.Fatal("no service_request_latency for svc1")
	}
	if agg.Mean > 200 {
		t.Fatalf("parent hop mean should exclude long async child CPU; got mean=%v", agg.Mean)
	}
}

func TestDownstreamFractionCPURetryChargesOverheadPerAttempt(t *testing.T) {
	eng := engine.NewEngine("test-run")
	policies := &config.Policies{
		Retries: &config.RetryPolicy{Enabled: true, MaxRetries: 2, Backoff: "constant", BaseMs: 0},
	}
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 8}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{
						Path: "/ingress", MeanCPUMs: 10, CPUSigmaMs: 0,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
						Downstream: []config.DownstreamCall{{
							To:                    "svc2:/api",
							Mode:                  "sync",
							FailureRate:           1,
							CallLatencyMs:         config.LatencySpec{Mean: 10, Sigma: 0},
							DownstreamFractionCPU: 0.5,
						}},
					},
				},
			},
			{
				ID: "svc2", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{
						Path: "/api", MeanCPUMs: 1, CPUSigmaMs: 0,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("init: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(policies), 7)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	RegisterHandlers(eng, state)
	inst, err := state.rm.SelectInstanceForService("svc1")
	if err != nil {
		t.Fatalf("instance: %v", err)
	}
	req := &models.Request{
		ID: "req-1", TraceID: "t1", ServiceName: "svc1", Endpoint: "/ingress",
		Status: models.RequestStatusPending, ArrivalTime: eng.GetSimTime(),
		Metadata: map[string]interface{}{"instance_id": inst.ID()},
	}
	eng.GetRunManager().AddRequest(req)
	eng.ScheduleAt(engine.EventTypeRequestStart, eng.GetSimTime(), req, "svc1", map[string]interface{}{
		"endpoint_path": "/ingress",
		"instance_id":   inst.ID(),
	})
	if err := eng.Run(5 * time.Second); err != nil {
		t.Fatalf("run: %v", err)
	}
	collector.Stop()
	got := sumMetricSeries(collector, metrics.MetricDownstreamCallerCPU)
	if got < 14 || got > 16 {
		t.Fatalf("expected ~15ms downstream_caller_cpu_ms (3 attempts × 5ms), got %v", got)
	}
}

func TestDownstreamFractionCPUDependencyFailureStillChargesCallerOverhead(t *testing.T) {
	eng := engine.NewEngine("test-run")
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "host-1", Cores: 8}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{
						Path: "/ingress", MeanCPUMs: 10, CPUSigmaMs: 0,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
						Downstream: []config.DownstreamCall{{
							To:                    "svc2:/api",
							Mode:                  "sync",
							FailureRate:           1,
							CallLatencyMs:         config.LatencySpec{Mean: 10, Sigma: 0},
							DownstreamFractionCPU: 1,
							Retryable:             ptrBool(false),
						}},
					},
				},
			},
			{
				ID: "svc2", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{
						Path: "/api", MeanCPUMs: 1, CPUSigmaMs: 0,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
					},
				},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("init: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 11)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	RegisterHandlers(eng, state)
	inst, err := state.rm.SelectInstanceForService("svc1")
	if err != nil {
		t.Fatalf("instance: %v", err)
	}
	req := &models.Request{
		ID: "req-1", TraceID: "t1", ServiceName: "svc1", Endpoint: "/ingress",
		Status: models.RequestStatusPending, ArrivalTime: eng.GetSimTime(),
		Metadata: map[string]interface{}{"instance_id": inst.ID()},
	}
	eng.GetRunManager().AddRequest(req)
	eng.ScheduleAt(engine.EventTypeRequestStart, eng.GetSimTime(), req, "svc1", map[string]interface{}{
		"endpoint_path": "/ingress",
		"instance_id":   inst.ID(),
	})
	if err := eng.Run(2 * time.Second); err != nil {
		t.Fatalf("run: %v", err)
	}
	collector.Stop()
	got := sumMetricSeries(collector, metrics.MetricDownstreamCallerCPU)
	if got < 9 || got > 11 {
		t.Fatalf("expected ~10ms caller overhead before dependency failure, got %v", got)
	}
}

func ptrBool(b bool) *bool { return &b }
