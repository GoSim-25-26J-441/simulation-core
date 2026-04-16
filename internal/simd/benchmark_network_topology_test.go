package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// Shared deterministic duration and seed so cross-scenario latency comparisons stay interpretable.
const benchNetDur = 400 * time.Millisecond
const benchNetSeed = int64(20260416)

func scenarioTwoZoneStacked(apiZone string, net *config.NetworkConfig) *config.Scenario {
	return &config.Scenario{
		Network: net,
		Hosts: []config.Host{
			{ID: "h-a", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h-b", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{
				ID: "edge", Replicas: 1, Model: "cpu",
				Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}},
				Endpoints: []config.Endpoint{{
					Path: "/in", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Downstream: []config.DownstreamCall{{
						To: "api:/x", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					}},
				}},
			},
			{
				ID: "api", Replicas: 1, Model: "cpu",
				Placement: &config.PlacementPolicy{RequiredZones: []string{apiZone}},
				Routing:   &config.RoutingPolicy{Strategy: "round_robin", LocalityZoneFrom: "client_zone"},
				Endpoints: []config.Endpoint{{
					Path: "/x", MeanCPUMs: 2, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				}},
			},
		},
		Workload: []config.WorkloadPattern{{
			From: "c", To: "edge:/in",
			Metadata: map[string]string{"client_zone": "zone-a"},
			Arrival:  config.ArrivalSpec{Type: "constant", RateRPS: 20},
		}},
	}
}

func TestDirectedCrossZoneLatencyRequiresForwardEdgeOrDefault(t *testing.T) {
	net := &config.NetworkConfig{
		CrossZoneLatencyMs: map[string]map[string]config.LatencySpec{
			// Only reverse direction (b->a); a->b hop should not match unless symmetric or default.
			"zone-b": {"zone-a": {Mean: 80, Sigma: 0}},
		},
	}
	rm, err := RunScenarioForMetrics(scenarioTwoZoneStacked("zone-b", net), benchNetDur, benchNetSeed, false)
	if err != nil {
		t.Fatal(err)
	}
	if rm.CrossZoneLatencyPenaltyMsTotal != 0 {
		t.Fatalf("directed-only map missing a->b: expected no cross-zone penalty, got total=%v", rm.CrossZoneLatencyPenaltyMsTotal)
	}
}

func TestSymmetricCrossZoneLatencyUsesReverseEdge(t *testing.T) {
	net := &config.NetworkConfig{
		SymmetricCrossZoneLatency: true,
		CrossZoneLatencyMs: map[string]map[string]config.LatencySpec{
			"zone-b": {"zone-a": {Mean: 80, Sigma: 0}},
		},
	}
	rm, err := RunScenarioForMetrics(scenarioTwoZoneStacked("zone-b", net), benchNetDur, benchNetSeed, false)
	if err != nil {
		t.Fatal(err)
	}
	if rm.CrossZoneLatencyPenaltyMsTotal <= 0 || rm.TopologyLatencyPenaltyMsTotal <= 0 {
		t.Fatalf("symmetric reverse: want cross-zone + topology penalties, cross=%v topo=%v",
			rm.CrossZoneLatencyPenaltyMsTotal, rm.TopologyLatencyPenaltyMsTotal)
	}
}

func TestSameZoneDifferentHostAppliesSameZoneLatency(t *testing.T) {
	net := &config.NetworkConfig{
		SameZoneLatencyMs: config.LatencySpec{Mean: 42, Sigma: 0},
	}
	sc := &config.Scenario{
		Network: net,
		Hosts: []config.Host{
			{ID: "h-a1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h-a2", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
		},
		Services: []config.Service{
			{
				ID: "edge", Replicas: 1, Model: "cpu",
				Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}},
				Endpoints: []config.Endpoint{{
					Path: "/in", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Downstream: []config.DownstreamCall{{
						To: "api:/x", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					}},
				}},
			},
			{
				ID: "api", Replicas: 1, Model: "cpu",
				Placement: &config.PlacementPolicy{
					RequiredZones:       []string{"zone-a"},
					AntiAffinityServices: []string{"edge"},
				},
				Routing: &config.RoutingPolicy{Strategy: "round_robin", LocalityZoneFrom: "client_zone"},
				Endpoints: []config.Endpoint{{
					Path: "/x", MeanCPUMs: 2, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				}},
			},
		},
		Workload: []config.WorkloadPattern{{
			From: "c", To: "edge:/in",
			Metadata: map[string]string{"client_zone": "zone-a"},
			Arrival:  config.ArrivalSpec{Type: "constant", RateRPS: 20},
		}},
	}
	rm, err := RunScenarioForMetrics(sc, benchNetDur, benchNetSeed, false)
	if err != nil {
		t.Fatal(err)
	}
	if rm.SameZoneLatencyPenaltyMsTotal <= 0 {
		t.Fatalf("expected same_zone_latency_ms penalty, got total=%v", rm.SameZoneLatencyPenaltyMsTotal)
	}
	if rm.TopologyLatencyPenaltyMsTotal <= 0 {
		t.Fatalf("expected topology rollups to include same-zone penalty, got %v", rm.TopologyLatencyPenaltyMsTotal)
	}
}

func TestExternalServiceAppliesNetworkExternalLatency(t *testing.T) {
	net := &config.NetworkConfig{
		ExternalLatencyMs: config.LatencySpec{Mean: 55, Sigma: 0},
	}
	sc := &config.Scenario{
		Network: net,
		Hosts: []config.Host{
			{ID: "h-a", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
		},
		Services: []config.Service{
			{
				ID: "edge", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{
					Path: "/in", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Downstream: []config.DownstreamCall{{
						To: "ext:/call", Mode: "sync", Kind: "external", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					}},
				}},
			},
			{
				ID: "ext", Kind: "external", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{
					Path: "/call", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				}},
			},
		},
		Workload: []config.WorkloadPattern{{
			From:     "c",
			To:       "edge:/in",
			Arrival:  config.ArrivalSpec{Type: "constant", RateRPS: 15},
		}},
	}
	rm, err := RunScenarioForMetrics(sc, benchNetDur, benchNetSeed, false)
	if err != nil {
		t.Fatal(err)
	}
	if rm.ExternalLatencyMsTotal <= 0 {
		t.Fatalf("expected external_latency overlay rollup, got total=%v", rm.ExternalLatencyMsTotal)
	}
	if rm.TopologyLatencyPenaltyMsTotal <= 0 {
		t.Fatalf("expected topology aggregate to count external hops, got %v", rm.TopologyLatencyPenaltyMsTotal)
	}
}
