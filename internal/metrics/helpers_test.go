package metrics

import (
	"testing"
	"time"
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
