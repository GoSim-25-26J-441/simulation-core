package improvement

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// fullScenarioIdentity mirrors the rich scenario used in batchspec/hash_test.go for identity checks.
func fullScenarioIdentity() *config.Scenario {
	return &config.Scenario{
		Metadata:         &config.ScenarioMetadata{SchemaVersion: "0.2.0"},
		SimulationLimits: &config.SimulationLimits{MaxTraceDepth: 10, MaxAsyncHops: 5},
		Hosts:            []config.Host{{ID: "h1", Cores: 4, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "api", Kind: "service", Role: "internal", Replicas: 2, Model: "cpu",
				CPUCores: 1, MemoryMB: 512,
				Scaling: &config.ScalingPolicy{Horizontal: true, VerticalCPU: false, VerticalMemory: true},
				Endpoints: []config.Endpoint{
					{
						Path: "/x", MeanCPUMs: 5, CPUSigmaMs: 1, DefaultMemoryMB: 10,
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.1},
						Downstream: []config.DownstreamCall{
							{
								To: "db:q", Mode: "sync", Kind: "db", Probability: 0.5,
								CallCountMean: 1, CallLatencyMs: config.LatencySpec{Mean: 2, Sigma: 0.5},
								TimeoutMs: 100, DownstreamFractionCPU: 0.1,
							},
						},
					},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{
				From: "c", SourceKind: "client", TrafficClass: "ingress", To: "api:/x",
				Arrival: config.ArrivalSpec{
					Type: "bursty", RateRPS: 10, StdDevRPS: 1,
					BurstRateRPS: 50, BurstDurationSeconds: 2, QuietDurationSeconds: 5,
				},
			},
		},
		Policies: &config.Policies{
			Autoscaling: &config.AutoscalingPolicy{Enabled: true, TargetCPUUtil: 0.7, ScaleStep: 1},
			Retries:     &config.RetryPolicy{Enabled: true, MaxRetries: 3, Backoff: "exponential", BaseMs: 100},
		},
	}
}

func TestConfigsMatchSemanticsRegression(t *testing.T) {
	a := fullScenarioIdentity()
	b := fullScenarioIdentity()
	if !configsMatch(a, b) {
		t.Fatal("identical scenarios should match")
	}

	c := fullScenarioIdentity()
	c.Services[0].Endpoints[0].Downstream[0].TimeoutMs = 999
	if configsMatch(a, c) {
		t.Fatal("downstream timeout_ms should break match")
	}

	d := fullScenarioIdentity()
	d.Workload[0].TrafficClass = "background"
	if configsMatch(a, d) {
		t.Fatal("traffic_class should break match")
	}

	e := fullScenarioIdentity()
	e.Services[0].Scaling.Horizontal = false
	if configsMatch(a, e) {
		t.Fatal("scaling policy should break match")
	}

	f := fullScenarioIdentity()
	f.Services[0].Endpoints[0].NetLatencyMs.Mean = 42
	if configsMatch(a, f) {
		t.Fatal("endpoint net latency should break match")
	}
}

func TestCloneScenarioPreservesConfigHash(t *testing.T) {
	orig := fullScenarioIdentity()
	cl := cloneScenario(orig)
	if batchspec.ConfigHash(orig) != batchspec.ConfigHash(cl) {
		t.Fatalf("cloneScenario should preserve ConfigHash; got %x vs %x",
			batchspec.ConfigHash(orig), batchspec.ConfigHash(cl))
	}
}
