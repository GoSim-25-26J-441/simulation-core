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
		Hosts: []config.Host{{ID: "h1", Cores: 4, Zone: "z1", Labels: map[string]string{"rack": "r1"}}},
		Services: []config.Service{
			{
				ID: "db", Kind: "database", Role: "datastore", Replicas: 1, Model: "db_latency",
				Placement: &config.PlacementPolicy{
					RequiredZones:        []string{"z1"},
					PreferredZones:       []string{"z2"},
					AffinityZones:        []string{"z1"},
					RequiredHostLabels:   map[string]string{"rack": "r1"},
					PreferredHostLabels:  map[string]string{"disk": "ssd"},
					AntiAffinityServices: []string{"cache"},
					SpreadAcrossZones:    true,
					MaxReplicasPerHost:   1,
				},
				Routing: &config.RoutingPolicy{
					Strategy:         "weighted_round_robin",
					LocalityZoneFrom: "client_zone",
					Weights:          map[string]float64{"db-instance-0": 0.8, "db-instance-1": 0.2},
				},
				Scaling: &config.ScalingPolicy{Horizontal: false, VerticalCPU: true, VerticalMemory: true},
				Endpoints: []config.Endpoint{
					{
						Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0,
						Routing: &config.RoutingPolicy{
							Strategy: "least_queue",
						},
						NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0},
						Downstream: []config.DownstreamCall{
							{To: "x:/p", Mode: "async", Kind: "db", Probability: 0.5, TimeoutMs: 100, DownstreamFractionCPU: 0.25},
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
			{From: "c", SourceKind: "client", TrafficClass: "ingress", Metadata: map[string]string{"client_zone": "zone-a"}, To: "x:/p", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
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
	if ds.Mode != "async" || ds.Kind != "db" || ds.Probability != 0.5 || ds.TimeoutMs != 100 || ds.DownstreamFractionCPU != 0.25 {
		t.Fatalf("downstream: %+v", ds)
	}
	if cl.Workload[0].TrafficClass != "ingress" || cl.Workload[0].SourceKind != "client" {
		t.Fatalf("workload: %+v", cl.Workload[0])
	}
	if cl.Workload[0].Metadata["client_zone"] != "zone-a" {
		t.Fatalf("workload metadata: %+v", cl.Workload[0].Metadata)
	}
	cl.Workload[0].Metadata["client_zone"] = "zone-b"
	if original.Workload[0].Metadata["client_zone"] != "zone-a" {
		t.Fatal("workload metadata map aliasing detected")
	}
	if cl.Hosts[0].Zone != "z1" || cl.Hosts[0].Labels["rack"] != "r1" {
		t.Fatalf("host topology lost: %+v", cl.Hosts[0])
	}
	if cl.Services[0].Routing == nil || cl.Services[0].Routing.Strategy != "weighted_round_robin" {
		t.Fatalf("service routing lost: %+v", cl.Services[0].Routing)
	}
	if cl.Services[0].Routing.LocalityZoneFrom != "client_zone" {
		t.Fatalf("service routing locality lost: %+v", cl.Services[0].Routing)
	}
	if cl.Services[0].Placement == nil || len(cl.Services[0].Placement.AffinityZones) != 1 || cl.Services[0].Placement.AffinityZones[0] != "z1" {
		t.Fatalf("service placement lost: %+v", cl.Services[0].Placement)
	}
	if len(cl.Services[0].Placement.RequiredZones) != 1 || cl.Services[0].Placement.RequiredZones[0] != "z1" {
		t.Fatalf("service placement required zones lost: %+v", cl.Services[0].Placement)
	}
	if !cl.Services[0].Placement.SpreadAcrossZones || cl.Services[0].Placement.MaxReplicasPerHost != 1 {
		t.Fatalf("service placement spread/max_per_host lost: %+v", cl.Services[0].Placement)
	}
	if cl.Services[0].Endpoints[0].Routing == nil || cl.Services[0].Endpoints[0].Routing.Strategy != "least_queue" {
		t.Fatalf("endpoint routing lost: %+v", cl.Services[0].Endpoints[0].Routing)
	}
	cl.Services[0].Kind = "changed"
	if original.Services[0].Kind != "database" {
		t.Fatal("clone mutation leaked to original")
	}
	cl.Services[0].Routing.Weights["db-instance-0"] = 0.01
	if original.Services[0].Routing.Weights["db-instance-0"] != 0.8 {
		t.Fatal("clone routing weights mutation leaked to original")
	}
	cl.Hosts[0].Labels["rack"] = "changed"
	if original.Hosts[0].Labels["rack"] != "r1" {
		t.Fatal("clone host labels mutation leaked to original")
	}
	cl.Services[0].Placement.RequiredHostLabels["rack"] = "changed"
	if original.Services[0].Placement.RequiredHostLabels["rack"] != "r1" {
		t.Fatal("clone placement labels mutation leaked to original")
	}
	cl.Services[0].Placement.PreferredHostLabels["disk"] = "hdd"
	if original.Services[0].Placement.PreferredHostLabels["disk"] != "ssd" {
		t.Fatal("clone preferred placement labels mutation leaked to original")
	}
}

func TestCloneScenarioPreservesQueueBehavior(t *testing.T) {
	original := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4}},
		Services: []config.Service{
			{
				ID: "consumer", Kind: "service", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{Path: "/handle", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
			{
				ID: "brk", Kind: "queue", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{
					Queue: &config.QueueBehavior{
						ConsumerTarget:         "consumer:/handle",
						Capacity:               50,
						ConsumerConcurrency:    2,
						MinConsumerConcurrency: 1,
						MaxConsumerConcurrency: 6,
						MaxRedeliveries:        3,
						DropPolicy:             "reject",
						AsyncFireAndForget:     true,
					},
				},
				Endpoints: []config.Endpoint{{Path: "/orders", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "consumer:/handle", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	cl := cloneScenario(original)
	q := cl.Services[1].Behavior.Queue
	if q == nil || q.Capacity != 50 || q.ConsumerConcurrency != 2 || !q.AsyncFireAndForget {
		t.Fatalf("queue behavior: %+v", q)
	}
	if q.MinConsumerConcurrency != 1 || q.MaxConsumerConcurrency != 6 {
		t.Fatalf("queue concurrency bounds: %+v", q)
	}
	q.Capacity = 999
	if original.Services[1].Behavior.Queue.Capacity != 50 {
		t.Fatal("clone mutation leaked to original queue")
	}
}

func TestCloneScenarioPreservesTopicBehavior(t *testing.T) {
	original := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 4}},
		Services: []config.Service{
			{
				ID: "consumer", Kind: "service", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{Path: "/handle", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
			{
				ID: "evt", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{
					Topic: &config.TopicBehavior{
						Partitions:         2,
						RetentionMs:        900000,
						Capacity:           5000,
						PublishAck:         "leader_ack",
						AsyncFireAndForget: true,
						Subscribers: []config.TopicSubscriber{
							{Name: "sub1", ConsumerGroup: "g1", ConsumerTarget: "consumer:/handle", ConsumerConcurrency: 3, MinConsumerConcurrency: 1, MaxConsumerConcurrency: 8, AckTimeoutMs: 7000, MaxRedeliveries: 2, DropPolicy: "reject"},
						},
					},
				},
				Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "consumer:/handle", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	cl := cloneScenario(original)
	tb := cl.Services[1].Behavior.Topic
	if tb == nil || tb.Partitions != 2 || tb.RetentionMs != 900000 || tb.Capacity != 5000 || !tb.AsyncFireAndForget {
		t.Fatalf("topic behavior: %+v", tb)
	}
	if len(tb.Subscribers) != 1 || tb.Subscribers[0].ConsumerGroup != "g1" || tb.Subscribers[0].ConsumerConcurrency != 3 {
		t.Fatalf("topic subscribers: %+v", tb.Subscribers)
	}
	if tb.Subscribers[0].MinConsumerConcurrency != 1 || tb.Subscribers[0].MaxConsumerConcurrency != 8 {
		t.Fatalf("topic subscriber concurrency bounds: %+v", tb.Subscribers[0])
	}
	tb.Subscribers[0].ConsumerConcurrency = 99
	if original.Services[1].Behavior.Topic.Subscribers[0].ConsumerConcurrency != 3 {
		t.Fatal("clone mutation leaked to original topic subscribers")
	}
}

func TestCloneScenarioPreservesDownstreamPartitionKeyFields(t *testing.T) {
	original := &config.Scenario{
		Services: []config.Service{{
			ID: "api", Replicas: 1, Model: "cpu",
			Endpoints: []config.Endpoint{{
				Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Downstream: []config.DownstreamCall{{
					To: "t:/x", Kind: "topic", Mode: "async",
					PartitionKey: "k1", PartitionKeyFrom: "tenant_id",
				}},
			}},
		}},
	}
	cl := cloneScenario(original)
	ds := cl.Services[0].Endpoints[0].Downstream[0]
	if ds.PartitionKey != "k1" || ds.PartitionKeyFrom != "tenant_id" {
		t.Fatalf("downstream partition fields: %+v", ds)
	}
}

func TestCloneScenarioPreservesNetworkTopologyFields(t *testing.T) {
	extLat := config.LatencySpec{Mean: 7, Sigma: 0.5}
	original := &config.Scenario{
		Network: &config.NetworkConfig{
			SymmetricCrossZoneLatency: true,
			SameHostLatencyMs:         config.LatencySpec{Mean: 1, Sigma: 0},
			SameZoneLatencyMs:         config.LatencySpec{Mean: 2, Sigma: 0},
			DefaultCrossZoneLatencyMs: config.LatencySpec{Mean: 9, Sigma: 0},
			ExternalLatencyMs:         config.LatencySpec{Mean: 40, Sigma: 0},
			CrossZoneLatencyMs: map[string]map[string]config.LatencySpec{
				"zone-a": {"zone-b": {Mean: 3, Sigma: 0}},
			},
		},
		Services: []config.Service{{
			ID: "ext", Kind: "external", Replicas: 1, Model: "cpu",
			ExternalNetworkLatencyMs: &extLat,
			Endpoints:                []config.Endpoint{{Path: "/", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{}}},
		}},
	}
	cl := cloneScenario(original)
	n := cl.Network
	if n == nil || !n.SymmetricCrossZoneLatency || n.SameHostLatencyMs.Mean != 1 || n.SameZoneLatencyMs.Mean != 2 {
		t.Fatalf("network fields: %+v", n)
	}
	if n.ExternalLatencyMs.Mean != 40 || n.DefaultCrossZoneLatencyMs.Mean != 9 {
		t.Fatalf("network defaults: %+v", n)
	}
	if n.CrossZoneLatencyMs["zone-a"]["zone-b"].Mean != 3 {
		t.Fatalf("cross zone map: %+v", n.CrossZoneLatencyMs)
	}
	if cl.Services[0].ExternalNetworkLatencyMs == nil || cl.Services[0].ExternalNetworkLatencyMs.Mean != 7 {
		t.Fatalf("external_network_latency_ms: %+v", cl.Services[0].ExternalNetworkLatencyMs)
	}
}
