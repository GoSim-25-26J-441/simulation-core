package metrics

import (
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestConvertToRunMetricsQueueWaitAndProcessingLatency(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	now := time.Now()
	svc := map[string]string{"service": "svcA", "endpoint": "/a"}
	collector.Record(MetricQueueWait, 1, now, svc)
	collector.Record(MetricQueueWait, 3, now, svc)
	collector.Record(MetricQueueWait, 5, now, svc)
	collector.Record(MetricServiceProcessingLatency, 10, now, svc)
	collector.Record(MetricServiceProcessingLatency, 20, now, svc)
	collector.Record(MetricServiceProcessingLatency, 30, now, svc)
	collector.Record(MetricServiceRequestLatency, 100, now, svc)
	collector.Record(MetricRequestCount, 1, now, map[string]string{"service": "svcA", "endpoint": "/a", LabelOrigin: OriginIngress})

	rm := ConvertToRunMetrics(collector, []map[string]string{{"service": "svcA"}}, nil)
	if rm == nil {
		t.Fatal("nil RunMetrics")
	}
	sm := rm.ServiceMetrics["svcA"]
	if sm == nil {
		t.Fatal("missing service svcA")
	}
	if sm.QueueWaitP50Ms == 0 || sm.QueueWaitMeanMs == 0 {
		t.Fatalf("expected queue wait aggregates, got p50=%v mean=%v", sm.QueueWaitP50Ms, sm.QueueWaitMeanMs)
	}
	if sm.ProcessingLatencyP50Ms == 0 || sm.ProcessingLatencyMeanMs == 0 {
		t.Fatalf("expected processing latency aggregates, got p50=%v mean=%v", sm.ProcessingLatencyP50Ms, sm.ProcessingLatencyMeanMs)
	}
	// Hop total (service_request_latency_ms) should differ from processing-only when both recorded.
	if sm.LatencyMean == sm.ProcessingLatencyMeanMs {
		t.Fatalf("service_request mean should not equal processing-only mean in this fixture")
	}
}

func TestConvertToRunMetricsIngressAndAttemptErrorRates(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	ing := map[string]string{"service": "s", "endpoint": "/", LabelOrigin: OriginIngress}
	collector.Record(MetricRequestCount, 1, ts, ing)
	collector.Record(MetricRequestCount, 1, ts, ing)
	collector.Record(MetricIngressLogicalFailure, 1, ts, EndpointErrorLabels(ing, ReasonTimeout))
	collector.Record(MetricRequestErrorCount, 1, ts, EndpointErrorLabels(ing, ReasonTimeout))
	collector.Record(MetricRequestErrorCount, 1, ts, EndpointErrorLabels(EndpointLabelsWithOrigin("s", "/x", OriginDownstream), ReasonTimeout))

	rm := ConvertToRunMetrics(collector, nil, nil)
	if rm.IngressFailedRequests != 1 {
		t.Fatalf("ingress failed want 1 got %d", rm.IngressFailedRequests)
	}
	if rm.IngressRequests != 2 {
		t.Fatalf("ingress requests want 2 got %d", rm.IngressRequests)
	}
	if rm.IngressErrorRate != 0.5 {
		t.Fatalf("ingress error rate want 0.5 got %v", rm.IngressErrorRate)
	}
	if rm.FailedRequests != 2 || rm.AttemptFailedRequests != 2 {
		t.Fatalf("attempt failures want 2 got failed=%d attempt=%d", rm.FailedRequests, rm.AttemptFailedRequests)
	}
	if rm.TotalRequests != 2 {
		t.Fatalf("total requests want 2 got %d", rm.TotalRequests)
	}
	if rm.AttemptErrorRate != 1.0 {
		t.Fatalf("attempt error rate want 1.0 got %v", rm.AttemptErrorRate)
	}
}

func TestConvertToRunMetricsQueueBrokerRollups(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	l1 := map[string]string{"service": "gw", "endpoint": "/x", "broker_service": "mq", "topic": "/a"}
	l2 := map[string]string{"service": "gw", "endpoint": "/y", "broker_service": "mq", "topic": "/b"}
	RecordQueueEnqueueCount(collector, 2, ts, l1)
	RecordQueueDequeueCount(collector, 1, ts, l1)
	RecordQueueDropCount(collector, 1, ts, l2)
	RecordQueueRedeliveryCount(collector, 1, ts, l1)
	RecordQueueDlqCount(collector, 1, ts, l2)
	RecordQueueDepth(collector, 3, ts, l1)
	RecordQueueDepth(collector, 7, ts, l2)

	rm := ConvertToRunMetrics(collector, nil, nil)
	if rm.QueueEnqueueCountTotal != 2 || rm.QueueDequeueCountTotal != 1 || rm.QueueDropCountTotal != 1 {
		t.Fatalf("queue counters: enqueue=%d dequeue=%d drop=%d", rm.QueueEnqueueCountTotal, rm.QueueDequeueCountTotal, rm.QueueDropCountTotal)
	}
	if rm.QueueRedeliveryCountTotal != 1 || rm.QueueDlqCountTotal != 1 {
		t.Fatalf("queue redelivery/dlq: %d %d", rm.QueueRedeliveryCountTotal, rm.QueueDlqCountTotal)
	}
	if rm.QueueDepthSum != 10 {
		t.Fatalf("queue_depth_sum want 10 got %v", rm.QueueDepthSum)
	}
}

func TestConvertToRunMetricsTopicBrokerRollups(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	l1 := map[string]string{"service": "gw", "endpoint": "/x", "broker_service": "evt", "topic": "/ev", "consumer_group": "g1"}
	l2 := map[string]string{"service": "gw", "endpoint": "/y", "broker_service": "evt", "topic": "/ev", "consumer_group": "g2"}
	RecordTopicPublishCount(collector, 1, ts, l1)
	RecordTopicDeliverCount(collector, 2, ts, l1)
	RecordTopicDropCount(collector, 1, ts, l2)
	RecordTopicRedeliveryCount(collector, 1, ts, l1)
	RecordTopicDlqCount(collector, 1, ts, l2)
	RecordTopicBacklogDepth(collector, 3, ts, l1)
	RecordTopicBacklogDepth(collector, 7, ts, l2)
	RecordTopicConsumerLag(collector, 3, ts, l1)
	RecordTopicConsumerLag(collector, 7, ts, l2)

	rm := ConvertToRunMetrics(collector, nil, nil)
	if rm.TopicPublishCountTotal != 1 || rm.TopicDeliverCountTotal != 2 || rm.TopicDropCountTotal != 1 {
		t.Fatalf("topic counters: pub=%d del=%d drop=%d", rm.TopicPublishCountTotal, rm.TopicDeliverCountTotal, rm.TopicDropCountTotal)
	}
	if rm.TopicRedeliveryCountTotal != 1 || rm.TopicDlqCountTotal != 1 {
		t.Fatalf("topic redelivery/dlq: %d %d", rm.TopicRedeliveryCountTotal, rm.TopicDlqCountTotal)
	}
	if rm.TopicBacklogDepthSum != 10 || rm.TopicConsumerLagSum != 10 {
		t.Fatalf("topic backlog/lag sum want 10 each got backlog=%v lag=%v", rm.TopicBacklogDepthSum, rm.TopicConsumerLagSum)
	}
	if rm.MaxTopicBacklogDepth != 7 || rm.MaxTopicConsumerLag != 7 {
		t.Fatalf("topic max backlog/lag want 7, got backlog=%v lag=%v", rm.MaxTopicBacklogDepth, rm.MaxTopicConsumerLag)
	}
}

func TestConvertToRunMetricsBrokerSnapshotAgesAndRates(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	lbl := map[string]string{"service": "gw", "endpoint": "/x", "broker_service": "mq", "topic": "/a"}
	RecordQueuePublishAttemptCount(collector, 10, ts, lbl)
	RecordQueueEnqueueCount(collector, 10, ts, lbl)
	RecordQueueDropCount(collector, 2, ts, lbl)
	RecordTopicPublishCount(collector, 8, ts, lbl)
	RecordTopicDropCount(collector, 1, ts, lbl)
	RecordTopicDeliverCount(collector, 7, ts, lbl)
	opts := &RunMetricsOptions{
		QueueBrokerSnapshots: []resource.QueueBrokerHealthSnapshot{
			{BrokerID: "mq", Topic: "/a", Depth: 3, OldestMessageAgeMs: 123},
		},
		TopicBrokerSnapshots: []resource.TopicBrokerHealthSnapshot{
			{BrokerID: "evt", Topic: "/ev", ConsumerGroup: "g1", Depth: 4, OldestMessageAgeMs: 456},
		},
	}
	rm := ConvertToRunMetrics(collector, nil, opts)
	if rm.QueueOldestMessageAgeMs != 123 || rm.TopicOldestMessageAgeMs != 456 {
		t.Fatalf("oldest ages: queue=%v topic=%v", rm.QueueOldestMessageAgeMs, rm.TopicOldestMessageAgeMs)
	}
	if rm.QueueDropRate != 0.2 {
		t.Fatalf("queue drop rate want 0.2 got %v", rm.QueueDropRate)
	}
	if rm.TopicDropRate != 0.125 {
		t.Fatalf("topic drop rate want 0.125 got %v", rm.TopicDropRate)
	}
}

func TestQueueDropRateUsesPublishAttemptsDenominator(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	lbl := map[string]string{"service": "gw", "endpoint": "/x", "broker_service": "mq", "topic": "/a"}

	// all publishes rejected: drop rate must be 1.0 (not 0 due to accepted denominator)
	RecordQueuePublishAttemptCount(collector, 4, ts, lbl)
	RecordQueueDropCount(collector, 4, ts, lbl)
	rm := ConvertToRunMetrics(collector, nil, nil)
	if rm.QueueDropRate != 1.0 {
		t.Fatalf("queue drop rate want 1.0 for fully rejected attempts, got %v", rm.QueueDropRate)
	}

	// mixed accepted + dropped attempts, including drop_oldest-style accepted path.
	RecordQueuePublishAttemptCount(collector, 6, ts, lbl)
	RecordQueueEnqueueCount(collector, 6, ts, lbl)
	RecordQueueDropCount(collector, 2, ts, lbl)
	rm = ConvertToRunMetrics(collector, nil, nil)
	if rm.QueueDropRate < 0 || rm.QueueDropRate > 1 {
		t.Fatalf("queue drop rate must stay in [0,1], got %v", rm.QueueDropRate)
	}
}

func TestConvertToRunMetricsEndpointRequestStats(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	l1 := map[string]string{"service": "api", "endpoint": "/a"}
	l2 := map[string]string{"service": "api", "endpoint": "/b"}
	collector.Record(MetricRequestCount, 10, ts, l1)
	collector.Record(MetricRequestErrorCount, 0, ts, l1)
	collector.Record(MetricRequestCount, 10, ts, l2)
	collector.Record(MetricRequestErrorCount, 5, ts, l2)
	rm := ConvertToRunMetrics(collector, nil, nil)
	if len(rm.EndpointRequestStats) != 2 {
		t.Fatalf("endpoint stats len=%d %+v", len(rm.EndpointRequestStats), rm.EndpointRequestStats)
	}
	by := make(map[string]int64)
	for _, es := range rm.EndpointRequestStats {
		by[es.EndpointPath] = es.ErrorCount
	}
	if by["/b"] != 5 || by["/a"] != 0 {
		t.Fatalf("unexpected errors %+v", by)
	}
}

func TestConvertToRunMetricsEndpointRequestStatsLatencyPointers(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	lbl := map[string]string{"service": "api", "endpoint": "/x"}
	collector.Record(MetricRequestCount, 5, ts, lbl)
	collector.Record(MetricServiceRequestLatency, 10, ts, lbl)
	collector.Record(MetricServiceRequestLatency, 20, ts, lbl)
	collector.Record(MetricServiceRequestLatency, 30, ts, lbl)
	rm := ConvertToRunMetrics(collector, nil, nil)
	var got *models.EndpointRequestStats
	for i := range rm.EndpointRequestStats {
		if rm.EndpointRequestStats[i].EndpointPath == "/x" {
			got = &rm.EndpointRequestStats[i]
			break
		}
	}
	if got == nil || got.LatencyP50Ms == nil {
		t.Fatalf("expected hop latency rollup for endpoint, got %+v", got)
	}
}

func TestConvertToRunMetricsInstanceRouteStats(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	RecordRouteSelectionCount(collector, 3, ts, CreateRouteSelectionLabels("api", "/x", "api-instance-0", "round_robin"))
	RecordRouteSelectionCount(collector, 2, ts, CreateRouteSelectionLabels("api", "/x", "api-instance-1", "round_robin"))
	rm := ConvertToRunMetrics(collector, nil, nil)
	if len(rm.InstanceRouteStats) != 2 {
		t.Fatalf("instance route stats len=%d %+v", len(rm.InstanceRouteStats), rm.InstanceRouteStats)
	}
	by := map[string]int64{}
	for _, st := range rm.InstanceRouteStats {
		by[st.InstanceID] = st.SelectionCount
	}
	if by["api-instance-0"] != 3 || by["api-instance-1"] != 2 {
		t.Fatalf("unexpected route counts %+v", by)
	}
}

func TestConvertToRunMetricsTopologyRoutingRollups(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	lbl := map[string]string{"service": "api", "endpoint": "/x", "instance": "api-0", "host": "h1", "host_zone": "zone-a", "requested_zone": "zone-a", LabelOrigin: OriginIngress}
	RecordLocalityRouteHitCount(collector, 8, ts, lbl)
	RecordLocalityRouteMissCount(collector, 2, ts, lbl)
	RecordSameZoneRequestCount(collector, 7, ts, lbl)
	RecordCrossZoneRequestCount(collector, 3, ts, lbl)

	rm := ConvertToRunMetrics(collector, nil, nil)
	if rm.LocalityHitRate != 0.8 {
		t.Fatalf("locality hit rate want 0.8 got %v", rm.LocalityHitRate)
	}
	if rm.SameZoneRequestCountTotal != 7 || rm.CrossZoneRequestCountTotal != 3 {
		t.Fatalf("same/cross totals got same=%d cross=%d", rm.SameZoneRequestCountTotal, rm.CrossZoneRequestCountTotal)
	}
	if rm.CrossZoneRequestFraction != 0.3 {
		t.Fatalf("cross zone fraction want 0.3 got %v", rm.CrossZoneRequestFraction)
	}
}

func TestConvertToRunMetricsCrossZonePenaltyRollups(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	lbl := map[string]string{"service": "api", "endpoint": "/x", LabelOrigin: OriginDownstream}
	RecordCrossZoneLatencyPenalty(collector, 100, ts, lbl)
	RecordCrossZoneLatencyPenalty(collector, 50, ts, lbl)
	RecordCrossZoneLatencyPenalty(collector, 50, ts, lbl)

	rm := ConvertToRunMetrics(collector, nil, nil)
	if rm.CrossZoneLatencyPenaltyMsTotal != 200 {
		t.Fatalf("penalty total got %v", rm.CrossZoneLatencyPenaltyMsTotal)
	}
	if rm.CrossZoneLatencyPenaltyMsMean != 200.0/3.0 {
		t.Fatalf("penalty mean got %v", rm.CrossZoneLatencyPenaltyMsMean)
	}
}

func TestConvertToRunMetricsTopologyPenaltyRollups(t *testing.T) {
	collector := NewCollector()
	collector.Start()
	ts := time.Now()
	lbl := map[string]string{"service": "api", "endpoint": "/x", LabelOrigin: OriginDownstream}
	RecordSameZoneLatencyPenalty(collector, 10, ts, lbl)
	RecordExternalLatencyPenalty(collector, 40, ts, lbl)
	RecordCrossZoneLatencyPenalty(collector, 50, ts, lbl)
	RecordTopologyLatencyPenalty(collector, 10, ts, lbl)
	RecordTopologyLatencyPenalty(collector, 40, ts, lbl)
	RecordTopologyLatencyPenalty(collector, 50, ts, lbl)

	rm := ConvertToRunMetrics(collector, nil, nil)
	if rm.SameZoneLatencyPenaltyMsTotal != 10 || rm.ExternalLatencyMsTotal != 40 || rm.CrossZoneLatencyPenaltyMsTotal != 50 {
		t.Fatalf("class totals sz=%v ext=%v cz=%v", rm.SameZoneLatencyPenaltyMsTotal, rm.ExternalLatencyMsTotal, rm.CrossZoneLatencyPenaltyMsTotal)
	}
	if rm.TopologyLatencyPenaltyMsTotal != 100 {
		t.Fatalf("topology total want 100 got %v", rm.TopologyLatencyPenaltyMsTotal)
	}
	if rm.TopologyLatencyPenaltyMsMean != 100.0/3.0 {
		t.Fatalf("topology mean got %v", rm.TopologyLatencyPenaltyMsMean)
	}
}
