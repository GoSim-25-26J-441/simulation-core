package improvement

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestGenerateBatchNeighborsDatabaseNoHorizontalWhenNilScaling(t *testing.T) {
	ep := []config.Endpoint{{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}}
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{ID: "db", Kind: "database", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 512, Endpoints: ep},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "db:/q", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)
	neighbors := GenerateBatchNeighbors(spec, base, base, nil)
	for _, n := range neighbors {
		for _, svc := range n.Services {
			if svc.ID == "db" && svc.Replicas != 1 {
				t.Fatalf("unexpected replica change for database without scaling policy: %+v", svc)
			}
		}
	}
}

func TestGenerateBatchNeighborsDatabaseHorizontalWhenExplicit(t *testing.T) {
	ep := []config.Endpoint{{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}}
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{
				ID: "db", Kind: "database", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 512, Endpoints: ep,
				Scaling: &config.ScalingPolicy{Horizontal: true, VerticalCPU: true, VerticalMemory: true},
			},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_SERVICE_SCALE_OUT: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_SERVICE_SCALE_OUT,
	}
	neighbors := GenerateBatchNeighbors(spec, base, base, nil)
	found := false
	for _, n := range neighbors {
		if n.Services[0].Replicas > 1 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected scale-out neighbor when database horizontal is explicit")
	}
}

func TestGenerateBatchNeighborsQueueConcurrencyActions(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 16, MemoryGB: 32}},
		Services: []config.Service{
			{
				ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 256,
				Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{
					ConsumerTarget: "svc:/p", ConsumerConcurrency: 2, MinConsumerConcurrency: 1, MaxConsumerConcurrency: 4,
				}},
				Endpoints: []config.Endpoint{{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
			{ID: "svc", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 256, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}}},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_QUEUE_SCALE_UP_CONCURRENCY:   {},
		simulationv1.BatchScalingAction_QUEUE_SCALE_DOWN_CONCURRENCY: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_QUEUE_SCALE_UP_CONCURRENCY,
		simulationv1.BatchScalingAction_QUEUE_SCALE_DOWN_CONCURRENCY,
	}
	neighbors := GenerateBatchNeighbors(spec, base, base, nil)
	var up, down bool
	for _, n := range neighbors {
		cc := n.Services[0].Behavior.Queue.ConsumerConcurrency
		if cc > 2 {
			up = true
		}
		if cc < 2 {
			down = true
		}
	}
	if !up || !down {
		t.Fatalf("expected queue concurrency up/down neighbors, got up=%v down=%v", up, down)
	}
}

func TestGenerateBatchNeighborsTopicSubscriberConcurrencyActions(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 16, MemoryGB: 32}},
		Services: []config.Service{
			{ID: "svc", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 256, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}}},
			{
				ID: "evt", Kind: "topic", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 256,
				Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					Subscribers: []config.TopicSubscriber{
						{Name: "sub", ConsumerGroup: "g1", ConsumerTarget: "svc:/p", ConsumerConcurrency: 2, MinConsumerConcurrency: 1, MaxConsumerConcurrency: 4},
					},
				}},
				Endpoints: []config.Endpoint{{Path: "/ev", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_TOPIC_SUBSCRIBER_SCALE_UP_CONCURRENCY:   {},
		simulationv1.BatchScalingAction_TOPIC_SUBSCRIBER_SCALE_DOWN_CONCURRENCY: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_TOPIC_SUBSCRIBER_SCALE_UP_CONCURRENCY,
		simulationv1.BatchScalingAction_TOPIC_SUBSCRIBER_SCALE_DOWN_CONCURRENCY,
	}
	neighbors := GenerateBatchNeighbors(spec, base, base, nil)
	var up, down bool
	for _, n := range neighbors {
		cc := n.Services[1].Behavior.Topic.Subscribers[0].ConsumerConcurrency
		if cc > 2 {
			up = true
		}
		if cc < 2 {
			down = true
		}
	}
	if !up || !down {
		t.Fatalf("expected topic subscriber concurrency up/down neighbors, got up=%v down=%v", up, down)
	}
}

func TestGenerateBatchNeighborsFiltersTopologyInfeasibleCandidates(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"}},
		Services: []config.Service{
			{
				ID:       "svc",
				Replicas: 1,
				Model:    "cpu",
				CPUCores: 1,
				MemoryMB: 256,
				Scaling:  &config.ScalingPolicy{Horizontal: true, VerticalCPU: true, VerticalMemory: true},
				Placement: &config.PlacementPolicy{
					RequiredZones: []string{"zone-b"},
				},
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "svc:/x", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_SERVICE_SCALE_OUT: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_SERVICE_SCALE_OUT,
	}
	neighbors := GenerateBatchNeighbors(spec, base, base, nil)
	if len(neighbors) != 0 {
		t.Fatalf("expected no neighbors due to topology infeasibility, got %d", len(neighbors))
	}
}

func TestGenerateBatchNeighborsHostScaleOutPreservesPreferredZone(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a", Labels: map[string]string{"rack": "r1"}},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-b", Labels: map[string]string{"rack": "r2"}},
		},
		Services: []config.Service{
			{
				ID:       "svc",
				Replicas: 1,
				Model:    "cpu",
				CPUCores: 1,
				MemoryMB: 256,
				Placement: &config.PlacementPolicy{
					RequiredZones: []string{"zone-b"},
				},
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "svc:/x", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_HOST_SCALE_OUT: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_HOST_SCALE_OUT,
	}
	neighbors := GenerateBatchNeighbors(spec, base, base, nil)
	if len(neighbors) == 0 {
		t.Fatal("expected host scale-out neighbor")
	}
	lastHost := neighbors[0].Hosts[len(neighbors[0].Hosts)-1]
	if lastHost.Zone != "zone-b" {
		t.Fatalf("expected new host to copy preferred/required zone-b template, got %s", lastHost.Zone)
	}
	if lastHost.Labels["rack"] != "r2" {
		t.Fatalf("expected new host labels from preferred template, got %+v", lastHost.Labels)
	}
}

func TestGenerateBatchNeighborsHostScaleInExploresAllHostRemovals(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
			{ID: "h3", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{
				ID:       "svc",
				Replicas: 2,
				Model:    "cpu",
				CPUCores: 1,
				MemoryMB: 256,
				Placement: &config.PlacementPolicy{
					RequiredZones: []string{"zone-b"},
				},
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "svc:/x", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.MinHosts = 2
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_HOST_SCALE_IN: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_HOST_SCALE_IN,
	}
	neighbors := GenerateBatchNeighbors(spec, base, base, nil)
	if len(neighbors) == 0 {
		t.Fatal("expected at least one feasible host scale-in neighbor")
	}
	// Removing last host h3 is infeasible (only one zone-b host left for 2 replicas with required zone-b).
	// Removing h1 is feasible and should be explored.
	foundRemoveH1 := false
	for _, n := range neighbors {
		hasH1 := false
		for _, h := range n.Hosts {
			if h.ID == "h1" {
				hasH1 = true
				break
			}
		}
		if !hasH1 {
			foundRemoveH1 = true
			break
		}
	}
	if !foundRemoveH1 {
		t.Fatalf("expected host scale-in to explore removal of non-last host h1; neighbors=%+v", neighbors)
	}
}

func TestHostScaleInAvoidsScarceRequiredZoneUnderCrossZonePressure(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{
			{ID: "ha", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "hb1", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
			{ID: "hb2", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{
				ID:       "svc",
				Replicas: 2,
				Model:    "cpu",
				CPUCores: 1,
				MemoryMB: 256,
				Placement: &config.PlacementPolicy{
					RequiredZones: []string{"zone-b"},
				},
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.MinHosts = 2
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_HOST_SCALE_IN: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_HOST_SCALE_IN,
	}
	lastMetrics := &simulationv1.RunMetrics{CrossZoneRequestFraction: 0.4}
	neighbors := GenerateBatchNeighbors(spec, base, base, lastMetrics)
	if len(neighbors) == 0 {
		t.Fatal("expected host scale-in neighbors")
	}
	first := neighbors[0]
	removedZoneA := true
	for _, h := range first.Hosts {
		if h.ID == "ha" {
			removedZoneA = false
		}
	}
	if !removedZoneA {
		t.Fatalf("expected first scale-in candidate to remove non-required zone host under cross-zone pressure, got %+v", first.Hosts)
	}
}

func TestHostScaleOutPrefersRequiredZoneWhenTopologyPressure(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{
			{ID: "ha", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "hb", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{
				ID:       "svc",
				Replicas: 3,
				Model:    "cpu",
				CPUCores: 1,
				MemoryMB: 256,
				Placement: &config.PlacementPolicy{
					RequiredZones: []string{"zone-b"},
				},
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_HOST_SCALE_OUT: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_HOST_SCALE_OUT,
	}
	lastMetrics := &simulationv1.RunMetrics{LocalityHitRate: 0.3, CrossZoneRequestFraction: 0.5}
	neighbors := GenerateBatchNeighbors(spec, base, base, lastMetrics)
	if len(neighbors) == 0 {
		t.Fatal("expected host scale-out neighbors")
	}
	added := neighbors[0].Hosts[len(neighbors[0].Hosts)-1]
	if added.Zone != "zone-b" {
		t.Fatalf("expected topology-aware scale-out in zone-b, got zone=%s", added.Zone)
	}
}

func TestServicePlacementCriticalityForHostRequiredLabels(t *testing.T) {
	cur := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h-critical", Cores: 8, MemoryGB: 16, Zone: "zone-a", Labels: map[string]string{"rack": "r1"}},
			{ID: "h-generic", Cores: 8, MemoryGB: 16, Zone: "zone-a", Labels: map[string]string{"rack": "r2"}},
		},
		Services: []config.Service{
			{
				ID:       "svc-labeled",
				Replicas: 2,
				Placement: &config.PlacementPolicy{
					RequiredZones:      []string{"zone-a"},
					RequiredHostLabels: map[string]string{"rack": "r1"},
				},
			},
		},
	}
	c0 := servicePlacementCriticalityForHost(cur, cur.Hosts[0])
	c1 := servicePlacementCriticalityForHost(cur, cur.Hosts[1])
	if c0 <= c1 {
		t.Fatalf("expected labeled host to be more critical, got c0=%d c1=%d", c0, c1)
	}
}

func TestHostScaleInKeepsPlacementCriticalHostWhenAlternativeExists(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h-critical", Cores: 8, MemoryGB: 16, Zone: "zone-a", Labels: map[string]string{"rack": "r1"}},
			{ID: "h-alt1", Cores: 8, MemoryGB: 16, Zone: "zone-a", Labels: map[string]string{"rack": "r2"}},
			{ID: "h-alt2", Cores: 8, MemoryGB: 16, Zone: "zone-a", Labels: map[string]string{"rack": "r3"}},
		},
		Services: []config.Service{
			{
				ID:       "svc",
				Replicas: 2,
				Model:    "cpu",
				CPUCores: 1,
				MemoryMB: 256,
				Placement: &config.PlacementPolicy{
					RequiredZones:      []string{"zone-a"},
					RequiredHostLabels: map[string]string{"rack": "r1"},
				},
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.MinHosts = 2
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_HOST_SCALE_IN: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_HOST_SCALE_IN,
	}
	neighbors := GenerateBatchNeighbors(spec, base, base, &simulationv1.RunMetrics{})
	if len(neighbors) == 0 {
		t.Fatal("expected host scale-in neighbors")
	}
	first := neighbors[0]
	removedCritical := true
	for _, h := range first.Hosts {
		if h.ID == "h-critical" {
			removedCritical = false
			break
		}
	}
	if removedCritical {
		t.Fatalf("expected first host scale-in candidate to keep placement-critical host, got %+v", first.Hosts)
	}
}

func TestServiceScaleOutPrioritizesLocalityRoutedRequiredZoneCoverageUnderTopologyPressure(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{
			{ID: "ha", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "hb", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{
				ID:       "svc-locality",
				Replicas: 1,
				Model:    "cpu",
				CPUCores: 1,
				MemoryMB: 256,
				Placement: &config.PlacementPolicy{
					RequiredZones: []string{"zone-a", "zone-b"},
				},
				Routing:   &config.RoutingPolicy{LocalityZoneFrom: "caller_zone"},
				Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
			{
				ID:        "svc-other",
				Replicas:  1,
				Model:     "cpu",
				CPUCores:  1,
				MemoryMB:  256,
				Endpoints: []config.Endpoint{{Path: "/y", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}}},
			},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_SERVICE_SCALE_OUT: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_SERVICE_SCALE_OUT,
	}
	lastMetrics := &simulationv1.RunMetrics{
		LocalityHitRate:          0.4,
		CrossZoneRequestFraction: 0.5,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc-locality", CpuUtilization: 0.25, MemoryUtilization: 0.25},
			{ServiceName: "svc-other", CpuUtilization: 0.95, MemoryUtilization: 0.95},
		},
	}
	neighbors := GenerateBatchNeighbors(spec, base, base, lastMetrics)
	if len(neighbors) == 0 {
		t.Fatal("expected scale-out neighbors")
	}
	first := neighbors[0]
	scaled := ""
	for i := range first.Services {
		if first.Services[i].Replicas > base.Services[i].Replicas {
			scaled = first.Services[i].ID
			break
		}
	}
	if scaled != "svc-locality" {
		t.Fatalf("expected topology-aware priority to scale svc-locality first, got %q", scaled)
	}
}
