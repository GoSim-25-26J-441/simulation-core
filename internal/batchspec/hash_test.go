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

func TestConfigHashHostZoneChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Hosts[0].Zone = "zone-a"
	b.Hosts[0].Zone = "zone-b"
	assertHashDiffers(t, a, b, "host.zone")
}

func TestConfigHashHostLabelsChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Hosts[0].Labels = map[string]string{"rack": "r1"}
	b.Hosts[0].Labels = map[string]string{"rack": "r2"}
	assertHashDiffers(t, a, b, "host.labels")
}

func TestConfigHashServiceKindChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Kind = "database"
	assertHashDiffers(t, a, b, "kind")
}

func TestConfigHashDownstreamPartitionKeyChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	b.Services[0].Endpoints[0].Downstream[0].PartitionKey = "tenant-a"
	assertHashDiffers(t, a, b, "downstream.partition_key")
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

func TestConfigHashServiceRoutingStrategyChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Services[0].Routing = &config.RoutingPolicy{Strategy: "round_robin"}
	b.Services[0].Routing = &config.RoutingPolicy{Strategy: "weighted_round_robin", Weights: map[string]float64{"api-instance-0": 0.9, "api-instance-1": 0.1}}
	assertHashDiffers(t, a, b, "service.routing.strategy")
}

func TestConfigHashServicePlacementChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Services[0].Placement = &config.PlacementPolicy{
		AffinityZones: []string{"zone-a"},
	}
	b.Services[0].Placement = &config.PlacementPolicy{
		AffinityZones: []string{"zone-b"},
	}
	assertHashDiffers(t, a, b, "service.placement.affinity_zones")
}

func TestConfigHashServicePlacementRequiredPreferredChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Services[0].Placement = &config.PlacementPolicy{
		RequiredZones:       []string{"zone-a"},
		PreferredZones:      []string{"zone-b"},
		PreferredHostLabels: map[string]string{"rack": "r1"},
		MaxReplicasPerHost:  1,
	}
	b.Services[0].Placement = &config.PlacementPolicy{
		RequiredZones:       []string{"zone-c"},
		PreferredZones:      []string{"zone-d"},
		PreferredHostLabels: map[string]string{"rack": "r2"},
		MaxReplicasPerHost:  2,
	}
	assertHashDiffers(t, a, b, "service.placement required/preferred fields")
}

func TestConfigHashEndpointRoutingOverrideChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Services[0].Endpoints[0].Routing = &config.RoutingPolicy{Strategy: "least_queue"}
	b.Services[0].Endpoints[0].Routing = &config.RoutingPolicy{Strategy: "sticky", StickyKeyFrom: "tenant"}
	assertHashDiffers(t, a, b, "endpoint.routing.strategy")
}

func TestConfigHashRoutingWeightChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Services[0].Routing = &config.RoutingPolicy{Strategy: "weighted_round_robin", Weights: map[string]float64{"api-instance-0": 0.9, "api-instance-1": 0.1}}
	b.Services[0].Routing = &config.RoutingPolicy{Strategy: "weighted_round_robin", Weights: map[string]float64{"api-instance-0": 0.7, "api-instance-1": 0.3}}
	assertHashDiffers(t, a, b, "service.routing.weights")
}

func TestConfigHashRoutingLocalityZoneFromChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Services[0].Routing = &config.RoutingPolicy{Strategy: "round_robin", LocalityZoneFrom: "client_zone"}
	b.Services[0].Routing = &config.RoutingPolicy{Strategy: "round_robin", LocalityZoneFrom: "origin_zone"}
	assertHashDiffers(t, a, b, "service.routing.locality_zone_from")
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

func TestConfigHashWorkloadMetadataChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Workload[0].Metadata = map[string]string{"client_zone": "zone-a"}
	b.Workload[0].Metadata = map[string]string{"client_zone": "zone-b"}
	assertHashDiffers(t, a, b, "workload metadata")
}

func TestConfigHashTopicBehaviorSubscriberConcurrencyChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	topicSvc := func(conc int) config.Service {
		return config.Service{
			ID: "evt", Kind: "topic", Role: "internal", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 256,
			Behavior: &config.ServiceBehavior{
				Topic: &config.TopicBehavior{
					Subscribers: []config.TopicSubscriber{
						{Name: "s", ConsumerGroup: "g1", ConsumerTarget: "api:/x", ConsumerConcurrency: conc},
					},
				},
			},
			Endpoints: []config.Endpoint{{Path: "/ev", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
		}
	}
	a.Services = append(a.Services, topicSvc(2))
	b.Services = append(b.Services, topicSvc(4))
	assertHashDiffers(t, a, b, "topic subscriber concurrency")
}

func TestConfigHashTopicRetentionChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	topicBase := func(ret int64) config.Service {
		return config.Service{
			ID: "evt", Kind: "topic", Role: "internal", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 256,
			Behavior: &config.ServiceBehavior{
				Topic: &config.TopicBehavior{
					RetentionMs: ret,
					Subscribers: []config.TopicSubscriber{
						{Name: "s", ConsumerGroup: "g1", ConsumerTarget: "api:/x", ConsumerConcurrency: 1},
					},
				},
			},
			Endpoints: []config.Endpoint{{Path: "/ev", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
		}
	}
	a.Services = append(a.Services, topicBase(600000))
	b.Services = append(b.Services, topicBase(1200000))
	assertHashDiffers(t, a, b, "topic retention_ms")
}

func TestConfigHashQueueConcurrencyBoundsChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	a.Services = append(a.Services, config.Service{
		ID: "mqb", Kind: "queue", Role: "internal", Replicas: 1, Model: "cpu",
		Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{
			ConsumerTarget: "api:/x", ConsumerConcurrency: 2, MinConsumerConcurrency: 1, MaxConsumerConcurrency: 4,
		}},
		Endpoints: []config.Endpoint{{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
	})
	b.Services = append(b.Services, config.Service{
		ID: "mqb", Kind: "queue", Role: "internal", Replicas: 1, Model: "cpu",
		Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{
			ConsumerTarget: "api:/x", ConsumerConcurrency: 2, MinConsumerConcurrency: 1, MaxConsumerConcurrency: 8,
		}},
		Endpoints: []config.Endpoint{{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
	})
	assertHashDiffers(t, a, b, "queue max_consumer_concurrency")
}

func TestConfigHashTopicSubscriberConcurrencyBoundsChange(t *testing.T) {
	a, b := scenarioV2(), scenarioV2()
	topic := func(maxc int) config.Service {
		return config.Service{
			ID: "evtb", Kind: "topic", Role: "internal", Replicas: 1, Model: "cpu",
			Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
				Subscribers: []config.TopicSubscriber{{
					Name: "s1", ConsumerGroup: "g1", ConsumerTarget: "api:/x", ConsumerConcurrency: 2, MinConsumerConcurrency: 1, MaxConsumerConcurrency: maxc,
				}},
			}},
			Endpoints: []config.Endpoint{{Path: "/ev", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
		}
	}
	a.Services = append(a.Services, topic(4))
	b.Services = append(b.Services, topic(6))
	assertHashDiffers(t, a, b, "topic subscriber max_consumer_concurrency")
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
