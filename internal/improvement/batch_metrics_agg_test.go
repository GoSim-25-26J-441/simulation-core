package improvement

import (
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/batchspec"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
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

func TestAggregateRunMetricsTopologySemantics(t *testing.T) {
	a := &simulationv1.RunMetrics{
		LocalityHitRate:               0.90,
		CrossZoneRequestFraction:      0.10,
		CrossZoneRequestCountTotal:    100,
		SameZoneRequestCountTotal:     300,
		CrossZoneLatencyPenaltyMsTotal: 1000,
		CrossZoneLatencyPenaltyMsMean:  8,
		SameZoneLatencyPenaltyMsTotal:  400,
		SameZoneLatencyPenaltyMsMean:   3,
		ExternalLatencyMsTotal:         200,
		ExternalLatencyMsMean:          4,
		TopologyLatencyPenaltyMsTotal:  1600,
		TopologyLatencyPenaltyMsMean:   12,
	}
	b := &simulationv1.RunMetrics{
		LocalityHitRate:               0.55,
		CrossZoneRequestFraction:      0.40,
		CrossZoneRequestCountTotal:    300,
		SameZoneRequestCountTotal:     100,
		CrossZoneLatencyPenaltyMsTotal: 2500,
		CrossZoneLatencyPenaltyMsMean:  15,
		SameZoneLatencyPenaltyMsTotal:  900,
		SameZoneLatencyPenaltyMsMean:   7,
		ExternalLatencyMsTotal:         600,
		ExternalLatencyMsMean:          10,
		TopologyLatencyPenaltyMsTotal:  4000,
		TopologyLatencyPenaltyMsMean:   20,
	}
	out := AggregateRunMetrics([]*simulationv1.RunMetrics{a, b})
	if out.GetLocalityHitRate() != 0.55 {
		t.Fatalf("expected min locality hit rate, got %v", out.GetLocalityHitRate())
	}
	if out.GetCrossZoneRequestFraction() != 0.40 {
		t.Fatalf("expected max cross-zone fraction, got %v", out.GetCrossZoneRequestFraction())
	}
	if out.GetTopologyLatencyPenaltyMsMean() != 20 {
		t.Fatalf("expected max topology latency mean, got %v", out.GetTopologyLatencyPenaltyMsMean())
	}
	if out.GetCrossZoneRequestCountTotal() != 200 || out.GetSameZoneRequestCountTotal() != 200 {
		t.Fatalf("expected averaged topology request totals, got cross=%v same=%v", out.GetCrossZoneRequestCountTotal(), out.GetSameZoneRequestCountTotal())
	}
	if out.GetCrossZoneLatencyPenaltyMsTotal() != 1750 || out.GetSameZoneLatencyPenaltyMsTotal() != 650 || out.GetExternalLatencyMsTotal() != 400 || out.GetTopologyLatencyPenaltyMsTotal() != 2800 {
		t.Fatalf("expected averaged topology penalty totals, got cross=%v same=%v external=%v topology=%v",
			out.GetCrossZoneLatencyPenaltyMsTotal(), out.GetSameZoneLatencyPenaltyMsTotal(), out.GetExternalLatencyMsTotal(), out.GetTopologyLatencyPenaltyMsTotal())
	}
}

func TestAggregateRunMetricsPreservesCrossZoneViolationForBatchScoring(t *testing.T) {
	base := &config.Scenario{
		Hosts:    []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{{ID: "svc", Replicas: 1, CPUCores: 1, MemoryMB: 256, Model: "cpu"}},
	}
	spec, err := batchspec.ParseBatchSpec(&simulationv1.BatchOptimizationConfig{
		MaxCrossZoneRequestFraction: 0.2,
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	seed1 := &simulationv1.RunMetrics{
		LatencyP95Ms:             10,
		LatencyP99Ms:             20,
		IngressThroughputRps:     100,
		CrossZoneRequestFraction: 0.05,
		LocalityHitRate:          0.95,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc", CpuUtilization: 0.5, MemoryUtilization: 0.5},
		},
	}
	seed2 := &simulationv1.RunMetrics{
		LatencyP95Ms:             10,
		LatencyP99Ms:             20,
		IngressThroughputRps:     100,
		CrossZoneRequestFraction: 0.35,
		LocalityHitRate:          0.60,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc", CpuUtilization: 0.5, MemoryUtilization: 0.5},
		},
	}
	agg := AggregateRunMetrics([]*simulationv1.RunMetrics{seed1, seed2})
	sc := ComputeBatchScore(spec, base, base, agg)
	if sc.CrossZoneViolation <= 0 {
		t.Fatalf("expected cross-zone guardrail violation to survive multi-seed aggregation, got %+v", sc)
	}
}

func TestAggregateRunMetricsPreservesTopologyMeanViolationForBatchScoring(t *testing.T) {
	base := &config.Scenario{
		Hosts:    []config.Host{{ID: "h1", Cores: 8, MemoryGB: 16}},
		Services: []config.Service{{ID: "svc", Replicas: 1, CPUCores: 1, MemoryMB: 256, Model: "cpu"}},
	}
	spec, err := batchspec.ParseBatchSpec(&simulationv1.BatchOptimizationConfig{
		MaxTopologyLatencyPenaltyMeanMs: 20,
	}, base)
	if err != nil {
		t.Fatal(err)
	}
	seed1 := &simulationv1.RunMetrics{
		LatencyP95Ms:                 10,
		LatencyP99Ms:                 20,
		IngressThroughputRps:         100,
		TopologyLatencyPenaltyMsMean: 8,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc", CpuUtilization: 0.5, MemoryUtilization: 0.5},
		},
	}
	seed2 := &simulationv1.RunMetrics{
		LatencyP95Ms:                 10,
		LatencyP99Ms:                 20,
		IngressThroughputRps:         100,
		TopologyLatencyPenaltyMsMean: 45,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc", CpuUtilization: 0.5, MemoryUtilization: 0.5},
		},
	}
	agg := AggregateRunMetrics([]*simulationv1.RunMetrics{seed1, seed2})
	sc := ComputeBatchScore(spec, base, base, agg)
	if sc.TopologyLatencyViolation <= 0 {
		t.Fatalf("expected topology-latency guardrail violation to survive multi-seed aggregation, got %+v", sc)
	}
}
