package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestCrossZoneNetworkPenaltyIncreasesRootLatencyVersusSameZone(t *testing.T) {
	net := &config.NetworkConfig{
		CrossZoneLatencyMs: map[string]map[string]config.LatencySpec{
			"zone-a": {"zone-b": {Mean: 100, Sigma: 0}},
		},
	}
	base := func(apiZone string) *config.Scenario {
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
					Routing:     &config.RoutingPolicy{Strategy: "round_robin", LocalityZoneFrom: "client_zone"},
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
	d := 400 * time.Millisecond
	seed := int64(2026)
	rmCross, err := RunScenarioForMetrics(base("zone-b"), d, seed, false)
	if err != nil {
		t.Fatal(err)
	}
	rmSame, err := RunScenarioForMetrics(base("zone-a"), d, seed, false)
	if err != nil {
		t.Fatal(err)
	}
	if rmCross.CrossZoneLatencyPenaltyMsTotal <= 0 || rmCross.CrossZoneLatencyPenaltyMsMean <= 0 {
		t.Fatalf("expected non-zero cross-zone penalty rollup, got total=%v mean=%v", rmCross.CrossZoneLatencyPenaltyMsTotal, rmCross.CrossZoneLatencyPenaltyMsMean)
	}
	if rmSame.CrossZoneLatencyPenaltyMsTotal != 0 {
		t.Fatalf("expected no cross-zone penalty for same-zone api, got total=%v", rmSame.CrossZoneLatencyPenaltyMsTotal)
	}
	if !(rmCross.LatencyMean > rmSame.LatencyMean+50) {
		t.Fatalf("expected materially higher root latency for cross-zone hop, cross=%v same=%v", rmCross.LatencyMean, rmSame.LatencyMean)
	}
}
