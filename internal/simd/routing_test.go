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

func TestRoutingMetricsRecordedOnSelection(t *testing.T) {
	eng := engine.NewEngine("routing-metrics")
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8}},
		Services: []config.Service{{
			ID: "svc1", Replicas: 2, Model: "cpu",
			Routing: &config.RoutingPolicy{Strategy: "least_queue"},
			Endpoints: []config.Endpoint{{
				Path: "/test", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
			}},
		}},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 42)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "svc1", map[string]interface{}{
		"service_id": "svc1", "endpoint_path": "/test",
	})
	if err := eng.Run(20 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	agg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricRouteSelectionCount, map[string]string{
		"service": "svc1", "endpoint": "/test", "strategy": "least_queue",
	})
	if agg == nil || agg.Count <= 0 {
		t.Fatalf("expected route_selection_count samples, got agg=%+v", agg)
	}
}

func TestLeastQueueDistributesIngressUnderCPUBacklog(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 16, MemoryGB: 32}},
		Services: []config.Service{{
			ID: "gateway", Replicas: 2, Model: "cpu", CPUCores: 1,
			Routing: &config.RoutingPolicy{Strategy: "least_queue"},
			Endpoints: []config.Endpoint{{
				Path: "/api", MeanCPUMs: 35, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
			}},
		}},
		Workload: []config.WorkloadPattern{{
			From: "c", To: "gateway:/api",
			Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 55},
		}},
	}
	dur := 1500 * time.Millisecond
	rm, err := RunScenarioForMetrics(sc, dur, 77, false)
	if err != nil {
		t.Fatal(err)
	}
	var sel0, sel1 int64
	for _, row := range rm.InstanceRouteStats {
		if row.ServiceName != "gateway" || row.EndpointPath != "/api" || row.Strategy != "least_queue" {
			continue
		}
		switch row.InstanceID {
		case "gateway-instance-0":
			sel0 = row.SelectionCount
		case "gateway-instance-1":
			sel1 = row.SelectionCount
		}
	}
	total := sel0 + sel1
	if total < 40 {
		t.Fatalf("expected enough routing samples, total=%d stats=%v", total, rm.InstanceRouteStats)
	}
	maxSel := sel0
	if sel1 > maxSel {
		maxSel = sel1
	}
	maxShare := float64(maxSel) / float64(total)
	if maxShare > 0.88 {
		t.Fatalf("routing skew too high (want spread across replicas): instance-0=%d instance-1=%d maxShare=%.3f",
			sel0, sel1, maxShare)
	}
	if rm.LatencyP95 <= 0 {
		t.Fatal("expected aggregate latency rollup")
	}
	if rm.LatencyP95 > 120_000 {
		t.Fatalf("latency unexpectedly extreme p95=%v ms", rm.LatencyP95)
	}
}

func TestRoutingStrategyAffectsTailLatency(t *testing.T) {
	base := func(strategy string, weights map[string]float64) *config.Scenario {
		return &config.Scenario{
			Hosts: []config.Host{{ID: "h1", Cores: 8}},
			Services: []config.Service{{
				ID: "svc", Replicas: 2, Model: "cpu", CPUCores: 1,
				Routing: &config.RoutingPolicy{Strategy: strategy, Weights: weights},
				Endpoints: []config.Endpoint{{
					Path: "/a", MeanCPUMs: 20, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				}},
			}},
			Workload: []config.WorkloadPattern{{
				From: "c", To: "svc:/a", Arrival: config.ArrivalSpec{Type: "constant", RateRPS: 70},
			}},
		}
	}
	dur := 2 * time.Second
	rr, err := RunScenarioForMetrics(base("round_robin", nil), dur, 99, false)
	if err != nil {
		t.Fatal(err)
	}
	// Bias nearly all traffic to instance-0; this should increase tail latency versus fair RR.
	wrr, err := RunScenarioForMetrics(base("weighted_round_robin", map[string]float64{
		"svc-instance-0": 9, "svc-instance-1": 1,
	}), dur, 99, false)
	if err != nil {
		t.Fatal(err)
	}
	if !(wrr.LatencyP95 > rr.LatencyP95) {
		t.Fatalf("expected weighted skew to increase p95 latency, rr=%v weighted=%v", rr.LatencyP95, wrr.LatencyP95)
	}
}

func TestRoutingMetricsRecordLocalityHitAndSameZoneCount(t *testing.T) {
	rm := resource.NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{{
			ID: "svc", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}},
			Routing:   &config.RoutingPolicy{Strategy: "round_robin", LocalityZoneFrom: "client_zone"},
			Endpoints: []config.Endpoint{{Path: "/test", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
		}},
	}
	if err := rm.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 11)
	if err != nil {
		t.Fatal(err)
	}
	req := &models.Request{ServiceName: "svc", Endpoint: "/test", Metadata: map[string]interface{}{"client_zone": "zone-a"}}
	if _, err := selectInstanceForRequest(state, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	hitAgg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricLocalityRouteHitCount, map[string]string{"service": "svc", "endpoint": "/test"})
	if hitAgg == nil || hitAgg.Sum < 1 {
		t.Fatalf("expected locality hit metric, got %+v", hitAgg)
	}
	sameAgg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricSameZoneRequestCount, map[string]string{"service": "svc", "endpoint": "/test"})
	if sameAgg == nil || sameAgg.Sum < 1 {
		t.Fatalf("expected same-zone count metric, got %+v", sameAgg)
	}
}

func TestRoutingMetricsRecordCrossZoneForDownstreamCallerCallee(t *testing.T) {
	rm := resource.NewManager()
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{
				ID: "caller", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}},
				Endpoints: []config.Endpoint{{Path: "/c", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{
				ID: "callee", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-b"}},
				Endpoints: []config.Endpoint{{Path: "/d", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
		},
	}
	if err := rm.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 22)
	if err != nil {
		t.Fatal(err)
	}
	req := &models.Request{
		ParentID:    "p1",
		ServiceName: "callee",
		Endpoint:    "/d",
		Metadata:    map[string]interface{}{"caller_instance_id": "caller-instance-0"},
	}
	if _, err := selectInstanceForRequest(state, req, time.Now()); err != nil {
		t.Fatal(err)
	}
	crossAgg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricCrossZoneRequestCount, map[string]string{"service": "callee", "endpoint": "/d"})
	if crossAgg == nil || crossAgg.Sum < 1 {
		t.Fatalf("expected cross-zone request metric, got %+v", crossAgg)
	}
}

func TestWorkloadMetadataDrivesLocalityRoutingMetrics(t *testing.T) {
	eng := engine.NewEngine("workload-locality-metrics")
	start := eng.GetSimTime()
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
		},
		Services: []config.Service{{
			ID: "svc", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 256,
			Routing:   &config.RoutingPolicy{Strategy: "round_robin", LocalityZoneFrom: "client_zone"},
			Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
		}},
		Workload: []config.WorkloadPattern{
			{
				From: "web", SourceKind: "client", TrafficClass: "ingress",
				Metadata: map[string]string{"client_zone": "zone-a"},
				To:       "svc:/x",
				Arrival:  config.ArrivalSpec{Type: "constant", RateRPS: 10},
			},
			{
				From: "mobile", SourceKind: "client", TrafficClass: "ingress",
				Metadata: map[string]string{"client_zone": "zone-b"},
				To:       "svc:/x",
				Arrival:  config.ArrivalSpec{Type: "constant", RateRPS: 10},
			},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 33)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	ws := NewWorkloadState("workload-locality-metrics", eng, start.Add(500*time.Millisecond), 33)
	if err := ws.Start(sc, start, false); err != nil {
		t.Fatal(err)
	}
	if err := eng.Run(500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	hit := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricLocalityRouteHitCount, map[string]string{"service": "svc", "endpoint": "/x"})
	miss := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricLocalityRouteMissCount, map[string]string{"service": "svc", "endpoint": "/x"})
	if hit == nil || hit.Sum <= 0 {
		t.Fatalf("expected locality_route_hit_count samples, got %+v", hit)
	}
	if miss == nil || miss.Sum <= 0 {
		t.Fatalf("expected locality_route_miss_count samples, got %+v", miss)
	}
}
