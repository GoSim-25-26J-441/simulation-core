package improvement

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestCloneScenarioPreservesV2Semantics(t *testing.T) {
	original := &config.Scenario{
		Metadata: &config.ScenarioMetadata{SchemaVersion: "0.2.0"},
		SimulationLimits: &config.SimulationLimits{
			MaxTraceDepth: 32,
			MaxAsyncHops:  8,
		},
		Hosts: []config.Host{{ID: "h1", Cores: 4}},
		Services: []config.Service{
			{
				ID: "db", Kind: "database", Role: "datastore", Replicas: 1, Model: "db_latency",
				Scaling: &config.ScalingPolicy{Horizontal: false, VerticalCPU: true, VerticalMemory: true},
				Endpoints: []config.Endpoint{
					{
						Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
						Downstream: []config.DownstreamCall{
							{To: "x:/p", Mode: "async", Kind: "db", Probability: 0.5, TimeoutMs: 100},
						},
					},
				},
			},
			{
				ID: "x", Kind: "service", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", SourceKind: "client", TrafficClass: "ingress", To: "x:/p", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	cl := cloneScenario(original)
	if cl.Metadata.SchemaVersion != "0.2.0" || cl.SimulationLimits.MaxTraceDepth != 32 {
		t.Fatalf("metadata/limits: %+v %+v", cl.Metadata, cl.SimulationLimits)
	}
	if cl.Services[0].Kind != "database" || cl.Services[0].Scaling == nil || cl.Services[0].Scaling.Horizontal {
		t.Fatalf("service scaling: %+v", cl.Services[0])
	}
	ds := cl.Services[0].Endpoints[0].Downstream[0]
	if ds.Mode != "async" || ds.Kind != "db" || ds.Probability != 0.5 || ds.TimeoutMs != 100 {
		t.Fatalf("downstream: %+v", ds)
	}
	if cl.Workload[0].TrafficClass != "ingress" || cl.Workload[0].SourceKind != "client" {
		t.Fatalf("workload: %+v", cl.Workload[0])
	}
	cl.Services[0].Kind = "changed"
	if original.Services[0].Kind != "database" {
		t.Fatal("clone mutation leaked to original")
	}
}
