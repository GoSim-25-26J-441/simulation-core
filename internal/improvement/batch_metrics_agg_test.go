package improvement

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestAggregateRunMetricsMerge(t *testing.T) {
	a := &simulationv1.RunMetrics{
		LatencyP50Ms:       10,
		LatencyP95Ms:       100,
		LatencyP99Ms:       200,
		LatencyMeanMs:      50,
		ThroughputRps:      10,
		TotalRequests:      100,
		SuccessfulRequests: 100,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", RequestCount: 100, ErrorCount: 0, LatencyP95Ms: 100, LatencyP99Ms: 200, LatencyMeanMs: 50, CpuUtilization: 0.4, MemoryUtilization: 0.5},
		},
	}
	b := &simulationv1.RunMetrics{
		LatencyP50Ms:       12,
		LatencyP95Ms:       300,
		LatencyP99Ms:       400,
		LatencyMeanMs:      800,
		ThroughputRps:      20,
		TotalRequests:      200,
		SuccessfulRequests: 200,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", RequestCount: 200, ErrorCount: 0, LatencyP95Ms: 300, LatencyP99Ms: 400, LatencyMeanMs: 800, CpuUtilization: 0.6, MemoryUtilization: 0.7},
		},
	}
	out := AggregateRunMetrics([]*simulationv1.RunMetrics{a, b})
	// Percentiles: max across runs (not average)
	if out.GetLatencyP50Ms() != 12 || out.GetLatencyP95Ms() != 300 || out.GetLatencyP99Ms() != 400 {
		t.Fatalf("latency percentiles want max across runs, got p50=%v p95=%v p99=%v", out.GetLatencyP50Ms(), out.GetLatencyP95Ms(), out.GetLatencyP99Ms())
	}
	// Mean: weighted by successful requests (100*50 + 200*800) / 300
	wantMean := (100*50 + 200*800) / 300.0
	if out.GetLatencyMeanMs() < wantMean-0.01 || out.GetLatencyMeanMs() > wantMean+0.01 {
		t.Fatalf("latency mean: got %v want %v", out.GetLatencyMeanMs(), wantMean)
	}
	if out.GetThroughputRps() != 15 {
		t.Fatalf("tput: %v", out.GetThroughputRps())
	}
	if len(out.ServiceMetrics) != 1 || out.ServiceMetrics[0].GetLatencyP95Ms() != 300 {
		t.Fatalf("service p95 merge: %+v", out.ServiceMetrics[0])
	}
	if out.ServiceMetrics[0].GetCpuUtilization() != 0.5 {
		t.Fatalf("service cpu merge: %+v", out.ServiceMetrics[0])
	}
}

func TestAggregateRunMetricsBrokerRiskSemantics(t *testing.T) {
	a := &simulationv1.RunMetrics{
		QueueEnqueueCountTotal:    100,
		QueueDropCountTotal:       2,
		TopicPublishCountTotal:    50,
		TopicDropCountTotal:       1,
		QueueDepthSum:             10,
		TopicBacklogDepthSum:      20,
		TopicConsumerLagSum:       30,
		MaxQueueDepth:             11,
		MaxTopicBacklogDepth:      22,
		MaxTopicConsumerLag:       33,
		QueueOldestMessageAgeMs:   1000,
		TopicOldestMessageAgeMs:   2000,
		QueueDropRate:             0.02,
		TopicDropRate:             0.01,
		QueueDlqCountTotal:        3,
		TopicDlqCountTotal:        4,
		QueueRedeliveryCountTotal: 7,
		TopicRedeliveryCountTotal: 8,
		QueueDequeueCountTotal:    90,
		TopicDeliverCountTotal:    45,
	}
	b := &simulationv1.RunMetrics{
		QueueEnqueueCountTotal:    300,
		QueueDropCountTotal:       10,
		TopicPublishCountTotal:    150,
		TopicDropCountTotal:       6,
		QueueDepthSum:             30,
		TopicBacklogDepthSum:      60,
		TopicConsumerLagSum:       90,
		MaxQueueDepth:             44,
		MaxTopicBacklogDepth:      55,
		MaxTopicConsumerLag:       66,
		QueueOldestMessageAgeMs:   9000,
		TopicOldestMessageAgeMs:   7000,
		QueueDropRate:             0.20,
		TopicDropRate:             0.10,
		QueueDlqCountTotal:        9,
		TopicDlqCountTotal:        11,
		QueueRedeliveryCountTotal: 13,
		TopicRedeliveryCountTotal: 15,
		QueueDequeueCountTotal:    280,
		TopicDeliverCountTotal:    140,
	}

	out := AggregateRunMetrics([]*simulationv1.RunMetrics{a, b})

	// Conservative risk fields: max across seeds.
	if out.GetMaxQueueDepth() != 44 || out.GetMaxTopicBacklogDepth() != 55 || out.GetMaxTopicConsumerLag() != 66 {
		t.Fatalf("max broker depth/lag aggregation incorrect: %+v", out)
	}
	if out.GetQueueOldestMessageAgeMs() != 9000 || out.GetTopicOldestMessageAgeMs() != 7000 {
		t.Fatalf("max oldest age aggregation incorrect: %+v", out)
	}
	if out.GetQueueDropRate() != 0.20 || out.GetTopicDropRate() != 0.10 {
		t.Fatalf("max drop-rate aggregation incorrect: %+v", out)
	}

	// Volume/count and sum-gauge rollups: averaged across seeds.
	if out.GetQueueEnqueueCountTotal() != 200 || out.GetTopicPublishCountTotal() != 100 {
		t.Fatalf("average count aggregation incorrect: %+v", out)
	}
	if out.GetQueueDepthSum() != 20 || out.GetTopicBacklogDepthSum() != 40 || out.GetTopicConsumerLagSum() != 60 {
		t.Fatalf("average sum-gauge aggregation incorrect: %+v", out)
	}
}
