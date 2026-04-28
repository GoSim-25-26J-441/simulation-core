package simd

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/interaction"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func setupBrokerTopologyState(t *testing.T, sc *config.Scenario) (*engine.Engine, *scenarioState, *resource.Manager, *metrics.Collector) {
	t.Helper()
	eng := engine.NewEngine("broker-topology")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(sc); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 44)
	if err != nil {
		t.Fatalf("newScenarioState: %v", err)
	}
	RegisterHandlers(eng, state)
	return eng, state, rm, collector
}

func TestQueueConsumerRecordsCrossZoneFromProducerTopology(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h-a", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h-b", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{ID: "producer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}}, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-b"}}, Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "queue", Kind: "queue", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}, Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{ConsumerTarget: "consumer:/consume", ConsumerConcurrency: 1}}},
		},
	}
	eng, _, _, collector := setupBrokerTopologyState(t, sc)
	parent := &models.Request{
		ID:          "p1",
		TraceID:     "t1",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: eng.GetSimTime(),
		Metadata:    map[string]interface{}{"instance_id": "producer-instance-0"},
	}
	eng.GetRunManager().AddRequest(parent)
	eng.ScheduleAt(engine.EventTypeQueueEnqueue, eng.GetSimTime(), parent, "queue", map[string]interface{}{
		"endpoint_path": "/q",
		"trace_depth":   1,
		"async_depth":   1,
	})
	if err := eng.Run(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	agg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricCrossZoneRequestCount, map[string]string{"service": "consumer", "endpoint": "/consume"})
	if agg == nil || agg.Sum < 1 {
		t.Fatalf("expected cross-zone queue consumer count, got %+v", agg)
	}
}

func TestTopicConsumerRecordsCrossZoneFromProducerTopology(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h-a", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h-b", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{ID: "producer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}}, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-b"}}, Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "topic", Kind: "topic", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}, Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{Subscribers: []config.TopicSubscriber{{Name: "sub", ConsumerGroup: "g1", ConsumerTarget: "consumer:/consume", ConsumerConcurrency: 1}}}}},
		},
	}
	eng, _, _, collector := setupBrokerTopologyState(t, sc)
	parent := &models.Request{
		ID:          "p2",
		TraceID:     "t2",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: eng.GetSimTime(),
		Metadata:    map[string]interface{}{"instance_id": "producer-instance-0"},
	}
	eng.GetRunManager().AddRequest(parent)
	eng.ScheduleAt(engine.EventTypeTopicPublish, eng.GetSimTime(), parent, "topic", map[string]interface{}{
		"endpoint_path": "/events",
		"trace_depth":   1,
		"async_depth":   1,
	})
	if err := eng.Run(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	agg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricCrossZoneRequestCount, map[string]string{"service": "consumer", "endpoint": "/consume"})
	if agg == nil || agg.Sum < 1 {
		t.Fatalf("expected cross-zone topic consumer count, got %+v", agg)
	}
}

func TestQueueConsumerUsesCallerHostZoneFallbackWhenProducerInstanceGone(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h-a", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h-b", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{ID: "producer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}}, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-b"}}, Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "queue", Kind: "queue", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}, Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{ConsumerTarget: "consumer:/consume", ConsumerConcurrency: 1}}},
		},
	}
	eng, state, rm, collector := setupBrokerTopologyState(t, sc)
	parent := &models.Request{
		ID:          "p3",
		TraceID:     "t3",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusCompleted,
		ArrivalTime: eng.GetSimTime(),
		Metadata:    map[string]interface{}{},
	}
	eng.GetRunManager().AddRequest(parent)
	shard := rm.GetBrokerQueue("queue", "/q", effectiveQueueForBroker(state, "queue"))
	_ = shard.Enqueue(&resource.QueuedMessage{
		ID:              "msg-1",
		EnqueueTime:     eng.GetSimTime(),
		ParentRequestID: "p3",
		TraceID:         "t3",
		Metadata: map[string]interface{}{
			"instance_id":      "producer-instance-gone",
			"caller_host_zone": "zone-a",
			"trace_depth":      1,
			"async_depth":      1,
		},
	})
	eng.ScheduleAt(engine.EventTypeQueueDequeue, eng.GetSimTime(), nil, "queue", map[string]interface{}{
		"broker_service": "queue",
		"broker_topic":   "/q",
	})
	if err := eng.Run(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	agg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricCrossZoneRequestCount, map[string]string{"service": "consumer", "endpoint": "/consume"})
	if agg == nil || agg.Sum < 1 {
		t.Fatalf("expected cross-zone count from caller_host_zone fallback, got %+v", agg)
	}
}

func TestQueueDelayedPublishUsesSnapshotCallerHostZoneAfterProducerRemoval(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h-a", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h-b", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{ID: "producer", Replicas: 2, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}}, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-b"}}, Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "queue", Kind: "queue", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}, Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{ConsumerTarget: "consumer:/consume", ConsumerConcurrency: 1}}},
		},
	}
	eng, state, rm, collector := setupBrokerTopologyState(t, sc)
	t0 := eng.GetSimTime()
	parent := &models.Request{
		ID:          "p4",
		TraceID:     "t4",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: t0,
		Metadata:    map[string]interface{}{"instance_id": "producer-instance-1"},
	}
	eng.GetRunManager().AddRequest(parent)
	ackAt := scheduleQueuePublishFromOverhead(state, eng, parent, interaction.ResolvedCall{ServiceID: "queue", Path: "/q", Call: config.DownstreamCall{}}, t0, 1, 1, false, 0, "lc-1", 50, downstreamCallerTopology{})
	if err := rm.ScaleServiceWithOptions("producer", 1, resource.ScaleServiceOptions{SimTime: t0, DrainTimeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	rm.ProcessDrainingInstances(t0.Add(2 * time.Millisecond))
	if err := eng.Run(ackAt.Sub(t0) + 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	agg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricCrossZoneRequestCount, map[string]string{"service": "consumer", "endpoint": "/consume"})
	if agg == nil || agg.Sum < 1 {
		t.Fatalf("expected delayed queue publish to preserve caller_host_zone fallback, got %+v", agg)
	}
}

func TestTopicDelayedPublishUsesSnapshotCallerHostZoneAfterProducerRemoval(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h-a", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h-b", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{ID: "producer", Replicas: 2, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}}, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-b"}}, Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "topic", Kind: "topic", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}, Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{Subscribers: []config.TopicSubscriber{{Name: "sub", ConsumerGroup: "g1", ConsumerTarget: "consumer:/consume", ConsumerConcurrency: 1}}}}},
		},
	}
	eng, state, rm, collector := setupBrokerTopologyState(t, sc)
	t0 := eng.GetSimTime()
	parent := &models.Request{
		ID:          "p5",
		TraceID:     "t5",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: t0,
		Metadata:    map[string]interface{}{"instance_id": "producer-instance-1"},
	}
	eng.GetRunManager().AddRequest(parent)
	ackAt := scheduleTopicPublishFromOverhead(state, eng, parent, interaction.ResolvedCall{ServiceID: "topic", Path: "/events", Call: config.DownstreamCall{}}, t0, 1, 1, false, 0, "lc-2", 50, downstreamCallerTopology{})
	if err := rm.ScaleServiceWithOptions("producer", 1, resource.ScaleServiceOptions{SimTime: t0, DrainTimeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	rm.ProcessDrainingInstances(t0.Add(2 * time.Millisecond))
	if err := eng.Run(ackAt.Sub(t0) + 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	agg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricCrossZoneRequestCount, map[string]string{"service": "consumer", "endpoint": "/consume"})
	if agg == nil || agg.Sum < 1 {
		t.Fatalf("expected delayed topic publish to preserve caller_host_zone fallback, got %+v", agg)
	}
}

func TestDirectAsyncDownstreamDelayedEventUsesSnapshotCallerTopologyAfterProducerRemoval(t *testing.T) {
	sc := &config.Scenario{
		Network: &config.NetworkConfig{
			CrossZoneLatencyMs: map[string]map[string]config.LatencySpec{
				"zone-a": {"zone-b": {Mean: 40, Sigma: 0}},
			},
		},
		Hosts: []config.Host{
			{ID: "h-a", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h-b", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{ID: "producer", Replicas: 2, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}}, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-b"}}, Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
	}
	eng, state, rm, collector := setupBrokerTopologyState(t, sc)
	t0 := eng.GetSimTime()
	parent := &models.Request{
		ID:          "p6",
		TraceID:     "t6",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: t0,
		Metadata: map[string]interface{}{
			"instance_id": "producer-instance-1",
		},
	}
	eng.GetRunManager().AddRequest(parent)
	scheduleDownstreamCallEvent(state, eng, parent, interaction.ResolvedCall{
		ServiceID: "consumer", Path: "/consume",
		Call: config.DownstreamCall{CallLatencyMs: config.LatencySpec{Mean: 50, Sigma: 0}},
	}, t0, 1, 0, true)
	if err := rm.ScaleServiceWithOptions("producer", 1, resource.ScaleServiceOptions{SimTime: t0, DrainTimeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	rm.ProcessDrainingInstances(t0.Add(2 * time.Millisecond))
	if err := eng.Run(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	agg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricCrossZoneRequestCount, map[string]string{"service": "consumer", "endpoint": "/consume"})
	if agg == nil || agg.Sum < 1 {
		t.Fatalf("expected direct async downstream cross-zone metric from event topology snapshot, got %+v", agg)
	}
	rmOut := metrics.ConvertToRunMetrics(collector, nil, nil)
	if rmOut.CrossZoneLatencyPenaltyMsTotal <= 0 {
		t.Fatalf("expected cross_zone_latency_penalty_ms rollups with network zone-a->zone-b, got total=%v mean=%v", rmOut.CrossZoneLatencyPenaltyMsTotal, rmOut.CrossZoneLatencyPenaltyMsMean)
	}
}

func TestDirectAsyncDownstreamUsesPrefilledCallerHostZoneFallback(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{
			{ID: "h-a", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h-b", Cores: 8, MemoryGB: 16, Zone: "zone-b"},
		},
		Services: []config.Service{
			{ID: "producer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}}, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-b"}}, Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
	}
	eng, state, _, collector := setupBrokerTopologyState(t, sc)
	t0 := eng.GetSimTime()
	parent := &models.Request{
		ID:          "p7",
		TraceID:     "t7",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: t0,
		Metadata: map[string]interface{}{
			"instance_id":      "producer-instance-gone",
			"caller_host_zone": "zone-a",
		},
	}
	eng.GetRunManager().AddRequest(parent)
	scheduleDownstreamCallEvent(state, eng, parent, interaction.ResolvedCall{
		ServiceID: "consumer", Path: "/consume",
		Call: config.DownstreamCall{CallLatencyMs: config.LatencySpec{Mean: 10, Sigma: 0}},
	}, t0, 1, 0, true)
	if err := eng.Run(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	agg := collector.GetOrComputeAggregationForLabelSubset(metrics.MetricCrossZoneRequestCount, map[string]string{"service": "consumer", "endpoint": "/consume"})
	if agg == nil || agg.Sum < 1 {
		t.Fatalf("expected direct async downstream cross-zone metric via prefilled caller_host_zone fallback, got %+v", agg)
	}
}

func TestQueueDelayedPublishPreservesSameZoneLatencyPenaltyAfterProducerRemoval(t *testing.T) {
	sc := &config.Scenario{
		Network: &config.NetworkConfig{
			SameZoneLatencyMs: config.LatencySpec{Mean: 31, Sigma: 0},
		},
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h3", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
		},
		Services: []config.Service{
			{ID: "producer", Replicas: 2, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}}, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}, AntiAffinityServices: []string{"producer"}}, Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "queue", Kind: "queue", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/q", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}, Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{ConsumerTarget: "consumer:/consume", ConsumerConcurrency: 1}}},
		},
	}
	eng, state, rm, collector := setupBrokerTopologyState(t, sc)
	t0 := eng.GetSimTime()
	parent := &models.Request{
		ID:          "p-sz-q",
		TraceID:     "t-sz-q",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: t0,
		Metadata:    map[string]interface{}{"instance_id": "producer-instance-1"},
	}
	eng.GetRunManager().AddRequest(parent)
	ackAt := scheduleQueuePublishFromOverhead(state, eng, parent, interaction.ResolvedCall{ServiceID: "queue", Path: "/q", Call: config.DownstreamCall{}}, t0, 1, 1, false, 0, "lc-sz-q", 50, downstreamCallerTopology{})
	if err := rm.ScaleServiceWithOptions("producer", 1, resource.ScaleServiceOptions{SimTime: t0, DrainTimeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	rm.ProcessDrainingInstances(t0.Add(2 * time.Millisecond))
	if err := eng.Run(ackAt.Sub(t0) + 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	rmOut := metrics.ConvertToRunMetrics(collector, nil, nil)
	if rmOut.SameZoneLatencyPenaltyMsTotal <= 0 {
		t.Fatalf("expected same_zone_latency_penalty_ms after delayed queue hop with stable caller_host_id, got total=%v mean=%v", rmOut.SameZoneLatencyPenaltyMsTotal, rmOut.SameZoneLatencyPenaltyMsMean)
	}
}

func TestTopicDelayedPublishPreservesSameZoneLatencyPenaltyAfterProducerRemoval(t *testing.T) {
	sc := &config.Scenario{
		Network: &config.NetworkConfig{
			SameZoneLatencyMs: config.LatencySpec{Mean: 29, Sigma: 0},
		},
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h3", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
		},
		Services: []config.Service{
			{ID: "producer", Replicas: 2, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}}, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}, AntiAffinityServices: []string{"producer"}}, Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "topic", Kind: "topic", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}, Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{Subscribers: []config.TopicSubscriber{{Name: "sub", ConsumerGroup: "g1", ConsumerTarget: "consumer:/consume", ConsumerConcurrency: 1}}}}},
		},
	}
	eng, state, rm, collector := setupBrokerTopologyState(t, sc)
	t0 := eng.GetSimTime()
	parent := &models.Request{
		ID:          "p-sz-t",
		TraceID:     "t-sz-t",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: t0,
		Metadata:    map[string]interface{}{"instance_id": "producer-instance-1"},
	}
	eng.GetRunManager().AddRequest(parent)
	ackAt := scheduleTopicPublishFromOverhead(state, eng, parent, interaction.ResolvedCall{ServiceID: "topic", Path: "/events", Call: config.DownstreamCall{}}, t0, 1, 1, false, 0, "lc-sz-t", 50, downstreamCallerTopology{})
	if err := rm.ScaleServiceWithOptions("producer", 1, resource.ScaleServiceOptions{SimTime: t0, DrainTimeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	rm.ProcessDrainingInstances(t0.Add(2 * time.Millisecond))
	if err := eng.Run(ackAt.Sub(t0) + 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	rmOut := metrics.ConvertToRunMetrics(collector, nil, nil)
	if rmOut.SameZoneLatencyPenaltyMsTotal <= 0 {
		t.Fatalf("expected same_zone_latency_penalty_ms after delayed topic hop with stable caller_host_id, got total=%v mean=%v", rmOut.SameZoneLatencyPenaltyMsTotal, rmOut.SameZoneLatencyPenaltyMsMean)
	}
}

func TestDirectAsyncDownstreamDelayedSameZoneLatencyPenaltyAfterProducerRemoval(t *testing.T) {
	sc := &config.Scenario{
		Network: &config.NetworkConfig{
			SameZoneLatencyMs: config.LatencySpec{Mean: 27, Sigma: 0},
		},
		Hosts: []config.Host{
			{ID: "h1", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h2", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
			{ID: "h3", Cores: 8, MemoryGB: 16, Zone: "zone-a"},
		},
		Services: []config.Service{
			{ID: "producer", Replicas: 2, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}}, Endpoints: []config.Endpoint{{Path: "/p", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumer", Replicas: 1, Model: "cpu", Placement: &config.PlacementPolicy{RequiredZones: []string{"zone-a"}, AntiAffinityServices: []string{"producer"}}, Endpoints: []config.Endpoint{{Path: "/consume", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
	}
	eng, state, rm, collector := setupBrokerTopologyState(t, sc)
	t0 := eng.GetSimTime()
	parent := &models.Request{
		ID:          "p-sz-d",
		TraceID:     "t-sz-d",
		ServiceName: "producer",
		Endpoint:    "/p",
		Status:      models.RequestStatusProcessing,
		ArrivalTime: t0,
		Metadata:    map[string]interface{}{"instance_id": "producer-instance-1"},
	}
	eng.GetRunManager().AddRequest(parent)
	scheduleDownstreamCallEvent(state, eng, parent, interaction.ResolvedCall{
		ServiceID: "consumer", Path: "/consume",
		Call: config.DownstreamCall{CallLatencyMs: config.LatencySpec{Mean: 50, Sigma: 0}},
	}, t0, 1, 0, true)
	if err := rm.ScaleServiceWithOptions("producer", 1, resource.ScaleServiceOptions{SimTime: t0, DrainTimeout: time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	rm.ProcessDrainingInstances(t0.Add(2 * time.Millisecond))
	if err := eng.Run(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	rmOut := metrics.ConvertToRunMetrics(collector, nil, nil)
	if rmOut.SameZoneLatencyPenaltyMsTotal <= 0 {
		t.Fatalf("expected same_zone_latency_penalty_ms on delayed async downstream with stable caller_host_id, got total=%v mean=%v", rmOut.SameZoneLatencyPenaltyMsTotal, rmOut.SameZoneLatencyPenaltyMsMean)
	}
}
