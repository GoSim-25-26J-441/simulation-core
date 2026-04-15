package improvement

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

func TestOrderNeighborsForExpansion_StressPrefersHigherCapacity(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 3, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)

	scaleUp := cloneScenario(base)
	scaleUp.Services[0].Replicas = 4
	scaleDown := cloneScenario(base)
	scaleDown.Services[0].Replicas = 2

	loStress := &simulationv1.RunMetrics{
		LatencyP95Ms: 900,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.85, MemoryUtilization: 0.5},
		},
	}
	ordered := orderNeighborsForExpansion(spec, base, loStress, []*config.Scenario{scaleDown, scaleUp})
	if len(ordered) != 2 {
		t.Fatalf("len=%d", len(ordered))
	}
	if ordered[0].Services[0].Replicas != 4 {
		t.Fatalf("under stress, expected scale-out neighbor first, got replicas=%d", ordered[0].Services[0].Replicas)
	}
}

func TestOrderNeighborsForExpansion_ColdPrefersLowerCapacity(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 3, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)

	scaleUp := cloneScenario(base)
	scaleUp.Services[0].Replicas = 4
	scaleDown := cloneScenario(base)
	scaleDown.Services[0].Replicas = 2

	calm := &simulationv1.RunMetrics{
		LatencyP95Ms: 100,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.55, MemoryUtilization: 0.55},
		},
	}
	ordered := orderNeighborsForExpansion(spec, base, calm, []*config.Scenario{scaleUp, scaleDown})
	if len(ordered) != 2 {
		t.Fatalf("len=%d", len(ordered))
	}
	if ordered[0].Services[0].Replicas != 2 {
		t.Fatalf("when not under stress, expected scale-in neighbor first, got replicas=%d", ordered[0].Services[0].Replicas)
	}
}

func TestOrderNeighborsForExpansion_BrokerLagStressPrefersHigherCapacity(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{ID: "svc1", Replicas: 3, CPUCores: 1, MemoryMB: 512, Model: "cpu"},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.MaxTopicConsumerLagSum = 10

	scaleUp := cloneScenario(base)
	scaleUp.Services[0].Replicas = 4
	scaleDown := cloneScenario(base)
	scaleDown.Services[0].Replicas = 2

	stressed := &simulationv1.RunMetrics{
		LatencyP95Ms:        100,
		TopicConsumerLagSum: 100,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", CpuUtilization: 0.55, MemoryUtilization: 0.55},
		},
	}
	ordered := orderNeighborsForExpansion(spec, base, stressed, []*config.Scenario{scaleDown, scaleUp})
	if ordered[0].Services[0].Replicas != 4 {
		t.Fatalf("under broker lag stress, expected scale-out neighbor first, got replicas=%d", ordered[0].Services[0].Replicas)
	}
}

func TestBrokerConsumerTargetServiceIndicesPrefersHigherPressureConsumer(t *testing.T) {
	sc := &config.Scenario{
		Services: []config.Service{
			{ID: "broker", Kind: "topic", Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
				Subscribers: []config.TopicSubscriber{
					{ConsumerGroup: "fast", ConsumerTarget: "consumer-fast:/handle"},
					{ConsumerGroup: "slow", ConsumerTarget: "consumer-slow:/handle"},
				},
			}}},
			{ID: "consumer-fast"},
			{ID: "consumer-slow"},
		},
	}
	m := &simulationv1.RunMetrics{
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "consumer-fast", CpuUtilization: 0.2, MemoryUtilization: 0.2},
			{ServiceName: "consumer-slow", CpuUtilization: 0.9, MemoryUtilization: 0.9, QueueLength: 10},
		},
	}
	idx := brokerConsumerTargetServiceIndices(sc, m)
	if len(idx) < 2 {
		t.Fatalf("expected both consumer services, got %d", len(idx))
	}
	if sc.Services[idx[0]].ID != "consumer-slow" {
		t.Fatalf("expected higher-pressure consumer first, got %s", sc.Services[idx[0]].ID)
	}
}

func TestGenerateBatchNeighbors_BrokerStressTargetsConsumerServicesFirst(t *testing.T) {
	base := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{
				ID: "broker", Kind: "topic", Replicas: 1, CPUCores: 1, MemoryMB: 256, Model: "cpu",
				Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					Subscribers: []config.TopicSubscriber{
						{ConsumerGroup: "fast", ConsumerTarget: "consumer-fast:/handle"},
						{ConsumerGroup: "slow", ConsumerTarget: "consumer-slow:/handle"},
					},
				}},
				Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1}}},
			},
			{ID: "consumer-fast", Replicas: 1, CPUCores: 1, MemoryMB: 256, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: config.LatencySpec{Mean: 1}}}},
			{ID: "consumer-slow", Replicas: 1, CPUCores: 1, MemoryMB: 256, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/handle", MeanCPUMs: 1, NetLatencyMs: config.LatencySpec{Mean: 1}}}},
			{ID: "other", Replicas: 1, CPUCores: 1, MemoryMB: 256, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/x", MeanCPUMs: 1, NetLatencyMs: config.LatencySpec{Mean: 1}}}},
		},
	}
	spec := batchspec.DefaultBatchSpec(base)
	spec.AllowedActions = map[simulationv1.BatchScalingAction]struct{}{
		simulationv1.BatchScalingAction_SERVICE_SCALE_OUT: {},
	}
	spec.AllowedActionsOrdered = []simulationv1.BatchScalingAction{
		simulationv1.BatchScalingAction_SERVICE_SCALE_OUT,
	}
	spec.MaxTopicConsumerLagSum = 10

	stressed := &simulationv1.RunMetrics{
		LatencyP95Ms:        100,
		TopicConsumerLagSum: 100,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "consumer-fast", CpuUtilization: 0.2, MemoryUtilization: 0.2},
			{ServiceName: "consumer-slow", CpuUtilization: 0.95, MemoryUtilization: 0.9, QueueLength: 12},
			{ServiceName: "other", CpuUtilization: 0.99, MemoryUtilization: 0.95},
		},
	}
	neighbors := GenerateBatchNeighbors(spec, base, base, stressed)
	if len(neighbors) == 0 {
		t.Fatal("expected neighbors")
	}
	first := neighbors[0]
	var scaled string
	for i := range first.Services {
		if first.Services[i].Replicas > base.Services[i].Replicas {
			scaled = first.Services[i].ID
			break
		}
	}
	if scaled != "consumer-slow" {
		t.Fatalf("expected first scale-out target to be slow topic consumer, got %q", scaled)
	}
}
