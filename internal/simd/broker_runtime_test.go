package simd

import (
	"strconv"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/engine"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/policy"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func sumMetricForLabel(collector *metrics.Collector, metricName, labelKey, labelValue string) float64 {
	sum := 0.0
	for _, lbl := range collector.GetLabelsForMetric(metricName) {
		if lbl[labelKey] != labelValue {
			continue
		}
		for _, p := range collector.GetTimeSeries(metricName, lbl) {
			sum += p.Value
		}
	}
	return sum
}

func topicDeliverByPartition(collector *metrics.Collector) map[int]float64 {
	out := map[int]float64{}
	for _, lbl := range collector.GetLabelsForMetric(metrics.MetricTopicDeliverCount) {
		p, err := strconv.Atoi(lbl["partition"])
		if err != nil {
			continue
		}
		for _, pt := range collector.GetTimeSeries(metrics.MetricTopicDeliverCount, lbl) {
			out[p] += pt.Value
		}
	}
	return out
}

// TestQueueBrokerAsyncPublishEnqueuesAndDequeues exercises one producer hop via downstream caller overhead
// into a queue service: enqueue → dequeue → consumer request completes (no redelivery).
func TestQueueBrokerAsyncPublishEnqueuesAndDequeues(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "api", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/pub",
						MeanCPUMs:    1,
						CPUSigmaMs:   0,
						NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
						Downstream: []config.DownstreamCall{
							{
								To:                    "mq:/orders",
								Mode:                  "sync",
								Kind:                  "queue",
								CallLatencyMs:         config.LatencySpec{Mean: 0, Sigma: 0},
								DownstreamFractionCPU: 0.25,
							},
						},
					},
				},
			},
			{
				ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{
					Queue: &config.QueueBehavior{
						ConsumerTarget:      "worker:/handle",
						DeliveryLatencyMs:   config.LatencySpec{Mean: 0, Sigma: 0},
						ConsumerConcurrency: 1,
						AckTimeoutMs:        5000,
						MaxRedeliveries:     3,
					},
				},
				Endpoints: []config.Endpoint{
					{Path: "/orders", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
				},
			},
			{
				ID: "worker", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/handle", MeanCPUMs: 2, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}

	eng := engine.NewEngine("queue-rt")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 424242)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{
		"service_id":    "api",
		"endpoint_path": "/pub",
	})
	if err := eng.Run(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	collector.Stop()

	if sumMetric(collector, metrics.MetricQueueEnqueueCount) < 1 {
		t.Fatalf("expected queue_enqueue_count, got %v", sumMetric(collector, metrics.MetricQueueEnqueueCount))
	}
	if sumMetric(collector, metrics.MetricQueueDequeueCount) < 1 {
		t.Fatalf("expected queue_dequeue_count, got %v", sumMetric(collector, metrics.MetricQueueDequeueCount))
	}
}

// TestTopicPublishFansOutToTwoSubscriberGroups checks one publish increments two subscriber backlogs and emits two deliver_count.
func TestTopicPublishFansOutToTwoSubscriberGroups(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "api", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{
						Path:         "/pub",
						MeanCPUMs:    1,
						CPUSigmaMs:   0,
						NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
						Downstream: []config.DownstreamCall{
							{
								To:                    "events:/events",
								Mode:                  "sync",
								Kind:                  "topic",
								CallLatencyMs:         config.LatencySpec{Mean: 0, Sigma: 0},
								DownstreamFractionCPU: 0.2,
							},
						},
					},
				},
			},
			{
				ID: "events", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{
					Topic: &config.TopicBehavior{
						Partitions:        1,
						RetentionMs:       600000,
						Capacity:          10000,
						DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
						Subscribers: []config.TopicSubscriber{
							{
								Name:                "a",
								ConsumerGroup:       "g1",
								ConsumerTarget:      "consumerA:/process",
								ConsumerConcurrency: 1,
								AckTimeoutMs:        5000,
							},
							{
								Name:                "b",
								ConsumerGroup:       "g2",
								ConsumerTarget:      "consumerB:/process",
								ConsumerConcurrency: 1,
								AckTimeoutMs:        5000,
							},
						},
					},
				},
				Endpoints: []config.Endpoint{
					{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
				},
			},
			{
				ID: "consumerA", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
				},
			},
			{
				ID: "consumerB", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{
					{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}

	eng := engine.NewEngine("topic-rt")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 424243)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{
		"service_id":    "api",
		"endpoint_path": "/pub",
	})
	if err := eng.Run(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	collector.Stop()

	if sumMetric(collector, metrics.MetricTopicPublishCount) < 1 {
		t.Fatalf("expected topic_publish_count, got %v", sumMetric(collector, metrics.MetricTopicPublishCount))
	}
	if sumMetric(collector, metrics.MetricTopicDeliverCount) < 2 {
		t.Fatalf("expected topic_deliver_count >= 2 for two groups, got %v", sumMetric(collector, metrics.MetricTopicDeliverCount))
	}
}

func assertTopicSnapshotDrainedAndLagZero(t *testing.T, snaps []resource.TopicBrokerHealthSnapshot, topic string) {
	t.Helper()
	for _, sn := range snaps {
		if sn.Topic != topic {
			continue
		}
		if sn.InFlight != 0 || sn.Depth != 0 || sn.ConsumerLag != 0 {
			t.Fatalf("expected drained with zero lag, got %+v", sn)
		}
	}
}

// twoTopicPublishesSameInstant schedules two topic_publish events at the same DES time so the second
// enqueue runs before any topic_dequeue from the first (backlog capacity applies to queued messages only).
func twoTopicPublishesSameInstant(eng *engine.Engine, parent *models.Request) {
	t0 := eng.GetSimTime()
	data := map[string]interface{}{
		"endpoint_path":        "/events",
		"delivery_ms":          0.0,
		"trace_depth":          0,
		"async_depth":          0,
		metaLogicalCallID:      "",
		metaRetryAttempt:       0,
		"workload_from":        "test",
		"workload_source_kind": "test",
	}
	eng.ScheduleAt(engine.EventTypeTopicPublish, t0, parent, "events", data)
	eng.ScheduleAt(engine.EventTypeTopicPublish, t0, parent, "events", data)
}

func TestTopicDropPoliciesNoPhantomLagAfterDrain(t *testing.T) {
	policies := []string{"reject", "drop_newest", "block"}
	for _, pol := range policies {
		pol := pol
		t.Run(pol, func(t *testing.T) {
			sc := &config.Scenario{
				Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
				Services: []config.Service{
					{
						ID: "events", Kind: "topic", Replicas: 1, Model: "cpu",
						Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
							Partitions:        1,
							Capacity:          1,
							DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
							Subscribers: []config.TopicSubscriber{{
								Name: "s1", ConsumerGroup: "g1", ConsumerTarget: "worker:/process",
								ConsumerConcurrency: 1, AckTimeoutMs: 5000, DropPolicy: pol,
							}},
						}},
						Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
					},
					{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
				},
			}
			eng := engine.NewEngine("topic-drop-" + pol)
			rm := resource.NewManager()
			if err := rm.InitializeFromScenario(sc); err != nil {
				t.Fatal(err)
			}
			collector := metrics.NewCollector()
			collector.Start()
			state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 9100)
			if err != nil {
				t.Fatal(err)
			}
			RegisterHandlers(eng, state)
			parent := &models.Request{
				ID: "tp-drop", ServiceName: "api", Endpoint: "/pub", TraceID: "tr",
				Status: models.RequestStatusProcessing, ArrivalTime: eng.GetSimTime(),
				Metadata: map[string]interface{}{"workload_from": "c"},
			}
			eng.GetRunManager().AddRequest(parent)
			twoTopicPublishesSameInstant(eng, parent)
			if err := eng.Run(200 * time.Millisecond); err != nil {
				t.Fatal(err)
			}
			collector.Stop()
			assertTopicSnapshotDrainedAndLagZero(t, rm.TopicBrokerHealthSnapshots(eng.GetSimTime()), "/events")
			if sumMetricForLabel(collector, metrics.MetricTopicDropCount, "consumer_group", "g1") < 1 {
				t.Fatalf("expected at least one subscriber drop, pol=%s", pol)
			}
		})
	}
}

func TestTopicDropOldestCommitsEvictedOffsetLagCoherent(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "events", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					Partitions:        1,
					Capacity:          1,
					DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Subscribers: []config.TopicSubscriber{{
						Name: "s1", ConsumerGroup: "g1", ConsumerTarget: "worker:/process",
						ConsumerConcurrency: 1, AckTimeoutMs: 5000, DropPolicy: "drop_oldest",
					}},
				}},
				Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
	}
	eng := engine.NewEngine("topic-drop-oldest")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 9101)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	parent := &models.Request{
		ID: "tp-do", ServiceName: "api", Endpoint: "/pub", TraceID: "tr",
		Status: models.RequestStatusProcessing, ArrivalTime: eng.GetSimTime(),
		Metadata: map[string]interface{}{"workload_from": "c"},
	}
	eng.GetRunManager().AddRequest(parent)
	twoTopicPublishesSameInstant(eng, parent)
	if err := eng.Run(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector.Stop()
	assertTopicSnapshotDrainedAndLagZero(t, rm.TopicBrokerHealthSnapshots(eng.GetSimTime()), "/events")
	if got := sumMetricForLabel(collector, metrics.MetricTopicDropCount, "reason", "drop_oldest"); got < 1 {
		t.Fatalf("expected drop_oldest counter, got %v", got)
	}
}

func TestTopicFanOutDropNewestVsDropOldestLagPerGroup(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "events", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					Partitions:        1,
					Capacity:          1,
					DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Subscribers: []config.TopicSubscriber{
						{Name: "dn", ConsumerGroup: "g_dn", ConsumerTarget: "wA:/process", ConsumerConcurrency: 1, AckTimeoutMs: 5000, DropPolicy: "drop_newest"},
						{Name: "do", ConsumerGroup: "g_do", ConsumerTarget: "wB:/process", ConsumerConcurrency: 1, AckTimeoutMs: 5000, DropPolicy: "drop_oldest"},
					},
				}},
				Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{ID: "wA", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "wB", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
	}
	eng := engine.NewEngine("topic-fanout-mix")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 9102)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	parent := &models.Request{
		ID: "tp-mix", ServiceName: "api", Endpoint: "/pub", TraceID: "tr",
		Status: models.RequestStatusProcessing, ArrivalTime: eng.GetSimTime(),
		Metadata: map[string]interface{}{"workload_from": "c"},
	}
	eng.GetRunManager().AddRequest(parent)
	twoTopicPublishesSameInstant(eng, parent)
	if err := eng.Run(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector.Stop()
	assertTopicSnapshotDrainedAndLagZero(t, rm.TopicBrokerHealthSnapshots(eng.GetSimTime()), "/events")
	dnDrops := sumMetricForLabel(collector, metrics.MetricTopicDropCount, "consumer_group", "g_dn")
	doDrops := sumMetricForLabel(collector, metrics.MetricTopicDropCount, "consumer_group", "g_do")
	if dnDrops < 1 {
		t.Fatalf("expected drop_newest group to record drops, got %v", dnDrops)
	}
	if doDrops < 1 {
		t.Fatalf("expected drop_oldest group to record drop_oldest evictions, got %v", doDrops)
	}
}

func TestTopicSlowSubscriberLagOnlyOnAffectedGroup(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "api", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{
					Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Downstream: []config.DownstreamCall{{To: "events:/events", Mode: "sync", Kind: "topic", CallCountMean: 8, CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
				}},
			},
			{
				ID: "events", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					Capacity: 10000, DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Subscribers: []config.TopicSubscriber{
						{Name: "slow", ConsumerGroup: "gslow", ConsumerTarget: "consumerSlow:/process", ConsumerConcurrency: 1, AckTimeoutMs: 100000},
						{Name: "fast", ConsumerGroup: "gfast", ConsumerTarget: "consumerFast:/process", ConsumerConcurrency: 4, AckTimeoutMs: 100000},
					},
				}},
				Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{ID: "consumerSlow", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 60, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumerFast", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	eng := engine.NewEngine("topic-lag")
	rm := resource.NewManager()
	_ = rm.InitializeFromScenario(scenario)
	collector := metrics.NewCollector()
	collector.Start()
	state, _ := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 77)
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(250 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector.Stop()
	slowLag := sumMetricForLabel(collector, metrics.MetricTopicConsumerLag, "consumer_group", "gslow")
	fastLag := sumMetricForLabel(collector, metrics.MetricTopicConsumerLag, "consumer_group", "gfast")
	if slowLag <= fastLag {
		t.Fatalf("expected slow group lag > fast group lag, got slow=%v fast=%v", slowLag, fastLag)
	}
}

func TestTopicAckTimeoutRedeliveryPerGroup(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "api", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Downstream: []config.DownstreamCall{{To: "events:/events", Kind: "topic", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
				}},
			},
			{
				ID: "events", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Subscribers: []config.TopicSubscriber{
						{Name: "slow", ConsumerGroup: "gslow", ConsumerTarget: "consumerSlow:/process", ConsumerConcurrency: 1, AckTimeoutMs: 5, MaxRedeliveries: 1},
						{Name: "fast", ConsumerGroup: "gfast", ConsumerTarget: "consumerFast:/process", ConsumerConcurrency: 1, AckTimeoutMs: 5000, MaxRedeliveries: 1},
					},
				}},
				Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{ID: "consumerSlow", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 100, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumerFast", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	eng := engine.NewEngine("topic-timeout")
	rm := resource.NewManager()
	_ = rm.InitializeFromScenario(scenario)
	collector := metrics.NewCollector()
	collector.Start()
	state, _ := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 79)
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(600 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector.Stop()
	if got := sumMetricForLabel(collector, metrics.MetricTopicRedeliveryCount, "consumer_group", "gslow"); got < 1 {
		t.Fatalf("expected redelivery for gslow, got %v", got)
	}
	if got := sumMetricForLabel(collector, metrics.MetricTopicRedeliveryCount, "consumer_group", "gfast"); got > 0 {
		t.Fatalf("expected no redelivery for gfast, got %v", got)
	}
}

func TestTopicAckTimeoutDLQPerGroup(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "api", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Downstream: []config.DownstreamCall{{To: "events:/events", Kind: "topic", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
				}},
			},
			{
				ID: "events", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Subscribers: []config.TopicSubscriber{
						{Name: "slow", ConsumerGroup: "gslow", ConsumerTarget: "consumerSlow:/process", ConsumerConcurrency: 1, AckTimeoutMs: 5, MaxRedeliveries: 0},
						{Name: "fast", ConsumerGroup: "gfast", ConsumerTarget: "consumerFast:/process", ConsumerConcurrency: 1, AckTimeoutMs: 5000, MaxRedeliveries: 0},
					},
				}},
				Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{ID: "consumerSlow", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 100, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "consumerFast", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	eng := engine.NewEngine("topic-dlq")
	rm := resource.NewManager()
	_ = rm.InitializeFromScenario(scenario)
	collector := metrics.NewCollector()
	collector.Start()
	state, _ := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 80)
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(600 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector.Stop()
	if got := sumMetricForLabel(collector, metrics.MetricTopicDlqCount, "consumer_group", "gslow"); got < 1 {
		t.Fatalf("expected dlq for gslow, got %v", got)
	}
	if got := sumMetricForLabel(collector, metrics.MetricTopicDlqCount, "consumer_group", "gfast"); got > 0 {
		t.Fatalf("expected no dlq for gfast, got %v", got)
	}
}

func TestQueueAckTimeoutRedeliveryAndDLQ(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Downstream: []config.DownstreamCall{{To: "mq:/orders", Kind: "queue", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			}}},
			{ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu", Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{
				ConsumerTarget: "worker:/handle", DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, ConsumerConcurrency: 1, AckTimeoutMs: 5, MaxRedeliveries: 0,
			}}, Endpoints: []config.Endpoint{{Path: "/orders", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/handle", MeanCPUMs: 100, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	eng := engine.NewEngine("queue-timeout")
	rm := resource.NewManager()
	_ = rm.InitializeFromScenario(scenario)
	collector := metrics.NewCollector()
	collector.Start()
	state, _ := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 81)
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(600 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector.Stop()
	if got := sumMetric(collector, metrics.MetricQueueDlqCount); got < 1 {
		t.Fatalf("expected queue dlq > 0, got %v", got)
	}
}

func TestQueueAckTimeoutRedelivery(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Downstream: []config.DownstreamCall{{To: "mq:/orders", Kind: "queue", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			}}},
			{ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu", Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{
				ConsumerTarget: "worker:/handle", DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, ConsumerConcurrency: 1, AckTimeoutMs: 5, MaxRedeliveries: 1,
			}}, Endpoints: []config.Endpoint{{Path: "/orders", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/handle", MeanCPUMs: 100, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	eng := engine.NewEngine("queue-redeliver")
	rm := resource.NewManager()
	_ = rm.InitializeFromScenario(scenario)
	collector := metrics.NewCollector()
	collector.Start()
	state, _ := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 82)
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(400 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector.Stop()
	if got := sumMetric(collector, metrics.MetricQueueRedeliveryCount); got < 1 {
		t.Fatalf("expected queue redelivery > 0, got %v", got)
	}
}

func TestQueueAckTimeoutLateCompletionIsIdempotent(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Downstream: []config.DownstreamCall{{To: "mq:/orders", Kind: "queue", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			}}},
			{ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu", Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{
				ConsumerTarget: "worker:/handle", DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, ConsumerConcurrency: 1, AckTimeoutMs: 5, MaxRedeliveries: 1,
			}}, Endpoints: []config.Endpoint{{Path: "/orders", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "worker", Replicas: 2, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/handle", MeanCPUMs: 20, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	eng := engine.NewEngine("queue-timeout-idempotent")
	rm := resource.NewManager()
	_ = rm.InitializeFromScenario(scenario)
	collector := metrics.NewCollector()
	collector.Start()
	state, _ := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 83)
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(400 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector.Stop()
	if got := sumMetric(collector, metrics.MetricQueueRedeliveryCount); got != 1 {
		t.Fatalf("expected exactly one redelivery, got %v", got)
	}
	if got := sumMetric(collector, metrics.MetricQueueDlqCount); got != 1 {
		t.Fatalf("expected exactly one dlq, got %v", got)
	}
	sh, ok := rm.BrokerQueues().GetShard("mq", "/orders")
	if !ok {
		t.Fatal("expected queue shard")
	}
	snap := sh.Snapshot(eng.GetSimTime())
	if snap.InFlight != 0 || snap.Depth != 0 {
		t.Fatalf("expected drained queue after timeout + late completion, got %+v", snap)
	}
}

func TestTopicAckTimeoutLateCompletionIsIdempotent(t *testing.T) {
	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{
				ID: "api", Replicas: 1, Model: "cpu",
				Endpoints: []config.Endpoint{{Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Downstream: []config.DownstreamCall{{To: "events:/events", Kind: "topic", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
				}},
			},
			{
				ID: "events", Kind: "topic", Replicas: 1, Model: "cpu",
				Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Subscribers: []config.TopicSubscriber{
						{Name: "slow", ConsumerGroup: "gslow", ConsumerTarget: "worker:/process", ConsumerConcurrency: 1, AckTimeoutMs: 5, MaxRedeliveries: 1},
					},
				}},
				Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			},
			{ID: "worker", Replicas: 2, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 20, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	eng := engine.NewEngine("topic-timeout-idempotent")
	rm := resource.NewManager()
	_ = rm.InitializeFromScenario(scenario)
	collector := metrics.NewCollector()
	collector.Start()
	state, _ := newScenarioState(scenario, rm, collector, policy.NewPolicyManager(nil), 84)
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(400 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector.Stop()
	if got := sumMetricForLabel(collector, metrics.MetricTopicRedeliveryCount, "consumer_group", "gslow"); got != 1 {
		t.Fatalf("expected exactly one topic redelivery, got %v", got)
	}
	if got := sumMetricForLabel(collector, metrics.MetricTopicDlqCount, "consumer_group", "gslow"); got != 1 {
		t.Fatalf("expected exactly one topic dlq, got %v", got)
	}
	sh, ok := rm.BrokerQueues().GetTopicSubscriberShard("events", "/events", "gslow")
	if !ok {
		t.Fatal("expected topic shard")
	}
	snap := sh.Snapshot(eng.GetSimTime())
	if snap.InFlight != 0 || snap.Depth != 0 {
		t.Fatalf("expected drained topic shard after timeout + late completion, got %+v", snap)
	}
}

func TestQueueAndTopicStateGaugeSumsDrainToZero(t *testing.T) {
	queueScenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, Downstream: []config.DownstreamCall{{To: "mq:/orders", Kind: "queue", Mode: "sync"}}}}},
			{ID: "mq", Kind: "queue", Replicas: 1, Model: "cpu", Behavior: &config.ServiceBehavior{Queue: &config.QueueBehavior{ConsumerTarget: "worker:/handle", ConsumerConcurrency: 1, DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}, Endpoints: []config.Endpoint{{Path: "/orders", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/handle", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	eng := engine.NewEngine("queue-depth-zero")
	rm := resource.NewManager()
	_ = rm.InitializeFromScenario(queueScenario)
	collector := metrics.NewCollector()
	collector.Start()
	state, _ := newScenarioState(queueScenario, rm, collector, policy.NewPolicyManager(nil), 85)
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	qRun := metrics.ConvertToRunMetrics(collector, nil, nil)
	if qRun.QueueDepthSum != 0 {
		t.Fatalf("expected queue_depth_sum=0 after drain, got %v", qRun.QueueDepthSum)
	}

	topicScenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, Downstream: []config.DownstreamCall{{To: "events:/events", Kind: "topic", Mode: "sync"}}}}},
			{ID: "events", Kind: "topic", Replicas: 1, Model: "cpu", Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, Subscribers: []config.TopicSubscriber{{Name: "s1", ConsumerGroup: "g1", ConsumerTarget: "worker:/process", ConsumerConcurrency: 1}}}}, Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
		},
		Workload: []config.WorkloadPattern{{From: "c", To: "api:/pub", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}}},
	}
	eng2 := engine.NewEngine("topic-depth-zero")
	rm2 := resource.NewManager()
	_ = rm2.InitializeFromScenario(topicScenario)
	collector2 := metrics.NewCollector()
	collector2.Start()
	state2, _ := newScenarioState(topicScenario, rm2, collector2, policy.NewPolicyManager(nil), 86)
	RegisterHandlers(eng2, state2)
	eng2.ScheduleAt(engine.EventTypeRequestArrival, eng2.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng2.Run(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector2.Stop()
	tRun := metrics.ConvertToRunMetrics(collector2, nil, nil)
	if tRun.TopicBacklogDepthSum != 0 || tRun.TopicConsumerLagSum != 0 {
		t.Fatalf("expected topic state sums=0 after drain, backlog=%v lag=%v", tRun.TopicBacklogDepthSum, tRun.TopicConsumerLagSum)
	}
}

func TestTopicPartitionAssignmentSeededReproducible(t *testing.T) {
	buildScenario := func() *config.Scenario {
		return &config.Scenario{
			Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
			Services: []config.Service{
				{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, Downstream: []config.DownstreamCall{{To: "events:/events", Kind: "topic", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}}}},
				{ID: "events", Kind: "topic", Replicas: 1, Model: "cpu", Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{Partitions: 2, DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}, Subscribers: []config.TopicSubscriber{{Name: "s1", ConsumerGroup: "g1", ConsumerTarget: "worker:/process", ConsumerConcurrency: 1}}}}, Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
				{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{Path: "/process", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			},
		}
	}
	run := func(seed int64) map[int]float64 {
		sc := buildScenario()
		eng := engine.NewEngine("topic-seeded")
		rm := resource.NewManager()
		if err := rm.InitializeFromScenario(sc); err != nil {
			t.Fatal(err)
		}
		collector := metrics.NewCollector()
		collector.Start()
		state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), seed)
		if err != nil {
			t.Fatal(err)
		}
		RegisterHandlers(eng, state)
		for i := 0; i < 6; i++ {
			eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
		}
		if err := eng.Run(2 * time.Second); err != nil {
			t.Fatal(err)
		}
		collector.Stop()
		return topicDeliverByPartition(collector)
	}
	a := run(2026)
	b := run(2026)
	if len(a) < 2 || len(b) < 2 {
		t.Fatalf("expected deliveries on both partitions, got a=%v b=%v", a, b)
	}
	if a[0] != b[0] || a[1] != b[1] {
		t.Fatalf("expected seeded reproducible partition split, got a=%v b=%v", a, b)
	}
}

// TestTopicRetentionExpiresQueuedWhileInFlightSecondMessage idle path: first in flight (slow CPU), second expires from retention DES event.
func TestTopicRetentionExpiresQueuedWhileInFlightSecondMessage(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{
				Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Downstream: []config.DownstreamCall{{
					To: "events:/events", Kind: "topic", Mode: "sync",
					CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				}},
			}}},
			{ID: "events", Kind: "topic", Replicas: 1, Model: "cpu", Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
				Partitions:        1,
				RetentionMs:       40,
				Capacity:          100,
				DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Subscribers: []config.TopicSubscriber{{
					Name: "s1", ConsumerGroup: "g1", ConsumerTarget: "worker:/process",
					ConsumerConcurrency: 1, AckTimeoutMs: 1e6, MaxRedeliveries: 5,
				}},
			}}, Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{
				Path: "/process", MeanCPUMs: 8000, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
			}}},
		},
	}
	eng := engine.NewEngine("topic-retention-idle")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 4242)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	t0 := eng.GetSimTime()
	eng.ScheduleAt(engine.EventTypeRequestArrival, t0, nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	eng.ScheduleAt(engine.EventTypeRequestArrival, t0.Add(5*time.Millisecond), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	collector.Stop()
	if sumMetricForLabel(collector, metrics.MetricTopicDropCount, "reason", "retention_expired") < 1 {
		t.Fatalf("expected retention_expired topic_drop_count, got %v", sumMetricForLabel(collector, metrics.MetricTopicDropCount, "reason", "retention_expired"))
	}
	sh, ok := rm.BrokerQueues().GetTopicSubscriberPartitionShard("events", "/events", 0, "g1")
	if !ok {
		t.Fatal("missing shard")
	}
	if sh.Depth() != 0 {
		t.Fatalf("expected backlog depth 0 after retention, got %d", sh.Depth())
	}
}

func TestTopicBrokerSnapshotNoStaleBacklogAfterRetention(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{
				Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Downstream: []config.DownstreamCall{{To: "events:/events", Kind: "topic", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			}}},
			{ID: "events", Kind: "topic", Replicas: 1, Model: "cpu", Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
				Partitions: 1, RetentionMs: 40, Capacity: 100,
				DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Subscribers: []config.TopicSubscriber{{
					Name: "s1", ConsumerGroup: "g1", ConsumerTarget: "worker:/process",
					ConsumerConcurrency: 1, AckTimeoutMs: 1e6, MaxRedeliveries: 5,
				}},
			}}, Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{
				Path: "/process", MeanCPUMs: 8000, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
			}}},
		},
	}
	eng := engine.NewEngine("topic-snapshot-ret")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 4243)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	t0 := eng.GetSimTime()
	eng.ScheduleAt(engine.EventTypeRequestArrival, t0, nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	eng.ScheduleAt(engine.EventTypeRequestArrival, t0.Add(5*time.Millisecond), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(300 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	simT := eng.GetSimTime()
	snaps := rm.TopicBrokerHealthSnapshots(simT)
	for _, sn := range snaps {
		if sn.ConsumerGroup == "g1" && sn.Topic == "/events" && sn.Depth > 0 {
			t.Fatalf("snapshot still shows backlog depth %d after retention", sn.Depth)
		}
	}
}

// TestTopicConsumerLagNonZeroWhileInFlightNotCommitted ensures lag (offset space) stays >0 until processing completes.
func TestTopicConsumerLagNonZeroWhileInFlightNotCommitted(t *testing.T) {
	sc := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 32, MemoryGB: 64}},
		Services: []config.Service{
			{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{
				Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Downstream: []config.DownstreamCall{{To: "events:/events", Kind: "topic", Mode: "sync", CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}},
			}}},
			{ID: "events", Kind: "topic", Replicas: 1, Model: "cpu", Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
				Partitions: 1, RetentionMs: 600000,
				DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				Subscribers: []config.TopicSubscriber{{
					Name: "s1", ConsumerGroup: "g1", ConsumerTarget: "worker:/process",
					ConsumerConcurrency: 1, AckTimeoutMs: 1e6, MaxRedeliveries: 5,
				}},
			}}, Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
			{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{
				Path: "/process", MeanCPUMs: 5000, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
			}}},
		},
	}
	eng := engine.NewEngine("topic-lag-inflight")
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(sc); err != nil {
		t.Fatal(err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), 4244)
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(eng, state)
	eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
	if err := eng.Run(3 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	simT := eng.GetSimTime()
	for _, sn := range rm.TopicBrokerHealthSnapshots(simT) {
		if sn.ConsumerGroup != "g1" {
			continue
		}
		if sn.InFlight < 1 {
			t.Fatalf("expected in_flight>=1 during slow processing, got %+v", sn)
		}
		if sn.ConsumerLag < 1 {
			t.Fatalf("expected consumer_lag>=1 while uncommitted in-flight, got %+v", sn)
		}
	}
}

func TestTopicDownstreamPartitionKeyStableAcrossRuns(t *testing.T) {
	build := func() *config.Scenario {
		return &config.Scenario{
			Hosts: []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
			Services: []config.Service{
				{ID: "api", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{
					Path: "/pub", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Downstream: []config.DownstreamCall{{
						To: "events:/events", Kind: "topic", Mode: "sync",
						PartitionKey:  "tenant-hotspot-x",
						CallLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					}},
				}}},
				{ID: "events", Kind: "topic", Replicas: 1, Model: "cpu", Behavior: &config.ServiceBehavior{Topic: &config.TopicBehavior{
					Partitions:        4,
					DeliveryLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
					Subscribers: []config.TopicSubscriber{{
						Name: "s1", ConsumerGroup: "g1", ConsumerTarget: "worker:/process", ConsumerConcurrency: 1,
					}},
				}}, Endpoints: []config.Endpoint{{Path: "/events", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0}}}},
				{ID: "worker", Replicas: 1, Model: "cpu", Endpoints: []config.Endpoint{{
					Path: "/process", MeanCPUMs: 2, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 0, Sigma: 0},
				}}},
			},
		}
	}
	run := func(seed int64) int {
		sc := build()
		eng := engine.NewEngine("topic-pk")
		rm := resource.NewManager()
		if err := rm.InitializeFromScenario(sc); err != nil {
			t.Fatal(err)
		}
		collector := metrics.NewCollector()
		collector.Start()
		state, err := newScenarioState(sc, rm, collector, policy.NewPolicyManager(nil), seed)
		if err != nil {
			t.Fatal(err)
		}
		RegisterHandlers(eng, state)
		eng.ScheduleAt(engine.EventTypeRequestArrival, eng.GetSimTime(), nil, "api", map[string]interface{}{"service_id": "api", "endpoint_path": "/pub"})
		if err := eng.Run(1 * time.Second); err != nil {
			t.Fatal(err)
		}
		collector.Stop()
		for _, lbl := range collector.GetLabelsForMetric(metrics.MetricTopicDeliverCount) {
			if lbl["partition"] != "" {
				p, _ := strconv.Atoi(lbl["partition"])
				return p
			}
		}
		return -1
	}
	a := run(777)
	b := run(777)
	if a < 0 || a != b {
		t.Fatalf("expected stable partition for partition_key, got a=%d b=%d", a, b)
	}
}
