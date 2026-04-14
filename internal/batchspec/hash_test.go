package batchspec

import (
	"testing"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// scenarioV2 returns a fully populated v2 scenario for identity regression tests.
func scenarioV2() *config.Scenario {
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

func TestConfigHashMetadataSchemaVersionChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Metadata.SchemaVersion = "0.9.9"
	assertHashDiffers(t, a, b, "metadata.schema_version")
}

func TestConfigHashIncludesHosts(t *testing.T) {
	s1 := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 2, MemoryGB: 8}},
		Services: []config.Service{
			{ID: "s", Replicas: 1, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	s2 := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4, MemoryGB: 8}},
		Services: []config.Service{
			{ID: "s", Replicas: 1, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	if ConfigHash(s1) == ConfigHash(s2) {
		t.Fatal("expected different hashes when host CPU differs")
	}
}

func TestConfigHashServiceKindChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Kind = "database"
	assertHashDiffers(t, a, b, "kind")
}

func TestConfigHashServiceRoleChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Role = "datastore"
	assertHashDiffers(t, a, b, "role")
}

func TestConfigHashScalingHorizontalChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Scaling.Horizontal = false
	assertHashDiffers(t, a, b, "scaling.horizontal")
}

func TestConfigHashScalingVerticalCPUChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Scaling.VerticalCPU = true
	assertHashDiffers(t, a, b, "scaling.vertical_cpu")
}

func TestConfigHashScalingVerticalMemoryChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Scaling.VerticalMemory = false
	assertHashDiffers(t, a, b, "scaling.vertical_memory")
}

func TestConfigHashEndpointMeanCPUMsChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Endpoints[0].MeanCPUMs = 99
	assertHashDiffers(t, a, b, "mean_cpu_ms")
}

func TestConfigHashEndpointNetLatencyChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Endpoints[0].NetLatencyMs.Mean = 42
	assertHashDiffers(t, a, b, "net_latency_ms")
}

func TestConfigHashDownstreamSyncToAsync(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Endpoints[0].Downstream[0].Mode = "async"
	assertHashDiffers(t, a, b, "downstream mode")
}

func TestConfigHashDownstreamProbabilityChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Endpoints[0].Downstream[0].Probability = 0.9
	assertHashDiffers(t, a, b, "probability")
}

func TestConfigHashDownstreamTimeoutChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Endpoints[0].Downstream[0].TimeoutMs = 999
	assertHashDiffers(t, a, b, "timeout_ms")
}

func TestConfigHashDownstreamKindRestToDB(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Endpoints[0].Downstream[0].Kind = "rest"
	assertHashDiffers(t, a, b, "downstream kind")
}

func TestConfigHashWorkloadTrafficClassChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Workload[0].TrafficClass = "background"
	assertHashDiffers(t, a, b, "traffic_class")
}

func TestConfigHashWorkloadSourceKindChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Workload[0].SourceKind = "batch"
	assertHashDiffers(t, a, b, "source_kind")
}

func TestConfigHashWorkloadBurstFieldsChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Workload[0].Arrival.BurstRateRPS = 99
	assertHashDiffers(t, a, b, "burst_rate_rps")
	c := scenarioV2()
	c.Workload[0].Arrival.BurstDurationSeconds = 99
	assertHashDiffers(t, a, c, "burst_duration_seconds")
	d := scenarioV2()
	d.Workload[0].Arrival.QuietDurationSeconds = 99
	assertHashDiffers(t, a, d, "quiet_duration_seconds")
}

func TestConfigHashQueueBehaviorCapacityChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Services = append(a.Services, config.Service{
		ID: "mq", Kind: "queue", Role: "internal", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 256,
		Behavior: &config.ServiceBehavior{
			Queue: &config.QueueBehavior{ConsumerTarget: "api:/x", Capacity: 100},
		},
		Endpoints: []config.Endpoint{{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
	})
	b.Services = append(b.Services, config.Service{
		ID: "mq", Kind: "queue", Role: "internal", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 256,
		Behavior: &config.ServiceBehavior{
			Queue: &config.QueueBehavior{ConsumerTarget: "api:/x", Capacity: 200},
		},
		Endpoints: []config.Endpoint{{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
	})
	assertHashDiffers(t, a, b, "queue capacity")
}

func TestConfigHashSimulationLimitsChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.SimulationLimits.MaxTraceDepth = 99
	assertHashDiffers(t, a, b, "max_trace_depth")
}

func TestConfigHashRetryBackoffAndBaseMsChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Policies.Retries.Backoff = "linear"
	assertHashDiffers(t, a, b, "backoff")
	c := scenarioV2()
	c.Policies.Retries.BaseMs = 999
	assertHashDiffers(t, a, c, "base_ms")
}

func TestScenarioSemanticsEqualIdentical(t *testing.T) {
	x := scenarioV2()
	y := scenarioV2()
	if !ScenarioSemanticsEqual(x, y) {
		t.Fatal("expected equal semantics for identical scenarios")
	}
}

func TestConfigHashServiceOrderIndependent(t *testing.T) {
	other := config.Service{
		ID: "other", Kind: "service", Role: "internal", Replicas: 1, Model: "cpu",
		CPUCores: 0.5, MemoryMB: 256,
		Endpoints: []config.Endpoint{{Path: "/y", MeanCPUMs: 1, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0.1}}},
	}
	s1 := scenarioV2()
	s2 := scenarioV2()
	api1 := s1.Services[0]
	api2 := s2.Services[0]
	s1.Services = []config.Service{other, api1}
	s2.Services = []config.Service{api2, other}
	if ConfigHash(s1) != ConfigHash(s2) {
		t.Fatal("expected same hash when services are permuted (canonical by ID)")
	}
}

func assertHashDiffers(t *testing.T, a, b *config.Scenario, what string) {
	t.Helper()
	if ConfigHash(a) == ConfigHash(b) {
		t.Fatalf("expected different hash when changing %s", what)
	}
}
