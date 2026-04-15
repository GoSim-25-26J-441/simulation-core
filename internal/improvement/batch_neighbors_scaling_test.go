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
