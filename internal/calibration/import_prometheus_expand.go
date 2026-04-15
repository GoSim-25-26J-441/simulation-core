package calibration

import (
	"fmt"
	"strconv"
	"strings"
)

// dispatchPrometheusSample maps one sim_* metric + labels into ObservedMetrics (mutates out).
func dispatchPrometheusSample(metric string, labels map[string]string, v float64, out *ObservedMetrics) error {
	m := strings.TrimSpace(strings.ToLower(metric))
	switch m {
	// Global / run-level
	case "sim_ingress_throughput_rps":
		out.Global.IngressThroughputRPS = F64(v)
	case "sim_ingress_error_rate":
		out.Global.IngressErrorRate = F64(v)
	case "sim_root_latency_p50_ms":
		out.Global.RootLatencyP50Ms = F64(v)
	case "sim_root_latency_p95_ms":
		out.Global.RootLatencyP95Ms = F64(v)
	case "sim_root_latency_p99_ms":
		out.Global.RootLatencyP99Ms = F64(v)
	case "sim_root_latency_mean_ms":
		out.Global.RootLatencyMeanMs = F64(v)
	case "sim_total_requests":
		out.Global.TotalRequests = I64(int64(v + 0.5))
	case "sim_ingress_requests":
		out.Global.IngressRequests = I64(int64(v + 0.5))
	case "sim_failed_requests":
		out.Global.FailedRequests = I64(int64(v + 0.5))
	case "sim_retry_attempts":
		out.Global.RetryAttempts = I64(int64(v + 0.5))
	case "sim_timeout_errors":
		out.Global.TimeoutErrors = I64(int64(v + 0.5))
	case "sim_ingress_failed_requests":
		out.Global.IngressFailedRequests = I64(int64(v + 0.5))
	// Service utilization (labels: service)
	case "sim_service_cpu_utilization":
		svc := labels["service"]
		if svc == "" {
			return fmt.Errorf("sim_service_cpu_utilization requires label service")
		}
		upsertServiceObs(out, svc, func(so *ServiceObservation) { so.CPUUtilization = F64(v) })
	case "sim_service_memory_utilization":
		svc := labels["service"]
		if svc == "" {
			return fmt.Errorf("sim_service_memory_utilization requires label service")
		}
		upsertServiceObs(out, svc, func(so *ServiceObservation) { so.MemoryUtilization = F64(v) })
	// Endpoint (labels: service, endpoint)
	case "sim_endpoint_latency_p50_ms", "sim_endpoint_latency_p95_ms", "sim_endpoint_latency_p99_ms", "sim_endpoint_latency_mean_ms",
		"sim_endpoint_queue_wait_p50_ms", "sim_endpoint_queue_wait_p95_ms", "sim_endpoint_queue_wait_p99_ms", "sim_endpoint_queue_wait_mean_ms",
		"sim_endpoint_processing_latency_p50_ms", "sim_endpoint_processing_latency_p95_ms", "sim_endpoint_processing_latency_p99_ms", "sim_endpoint_processing_latency_mean_ms",
		"sim_endpoint_request_count", "sim_endpoint_error_count":
		svc := labels["service"]
		ep := labels["endpoint"]
		if svc == "" || ep == "" {
			return fmt.Errorf("%s requires labels service and endpoint", metric)
		}
		e := upsertEndpointObs(out, svc, ep)
		switch m {
		case "sim_endpoint_latency_p50_ms":
			e.LatencyP50Ms = F64(v)
		case "sim_endpoint_latency_p95_ms":
			e.LatencyP95Ms = F64(v)
		case "sim_endpoint_latency_p99_ms":
			e.LatencyP99Ms = F64(v)
		case "sim_endpoint_latency_mean_ms":
			e.LatencyMeanMs = F64(v)
		case "sim_endpoint_queue_wait_p50_ms":
			e.QueueWaitP50Ms = F64(v)
		case "sim_endpoint_queue_wait_p95_ms":
			e.QueueWaitP95Ms = F64(v)
		case "sim_endpoint_queue_wait_p99_ms":
			e.QueueWaitP99Ms = F64(v)
		case "sim_endpoint_queue_wait_mean_ms":
			e.QueueWaitMeanMs = F64(v)
		case "sim_endpoint_processing_latency_p50_ms":
			e.ProcessingLatencyP50Ms = F64(v)
		case "sim_endpoint_processing_latency_p95_ms":
			e.ProcessingLatencyP95Ms = F64(v)
		case "sim_endpoint_processing_latency_p99_ms":
			e.ProcessingLatencyP99Ms = F64(v)
		case "sim_endpoint_processing_latency_mean_ms":
			e.ProcessingLatencyMeanMs = F64(v)
		case "sim_endpoint_request_count":
			e.RequestCount = I64(int64(v + 0.5))
		case "sim_endpoint_error_count":
			e.ErrorCount = I64(int64(v + 0.5))
		}
	// Queue broker shard (labels: broker_service, topic)
	case "sim_queue_publish_attempts", "sim_queue_drops", "sim_queue_depth", "sim_queue_oldest_age_ms", "sim_queue_dlq_count":
		bs, top := labels["broker_service"], labels["topic"]
		if bs == "" || top == "" {
			return fmt.Errorf("%s requires broker_service and topic labels", metric)
		}
		q := upsertQueueBrokerObs(out, bs, top)
		switch m {
		case "sim_queue_publish_attempts":
			q.QueuePublishAttemptCount = I64(int64(v + 0.5))
		case "sim_queue_drops":
			q.DropCount = I64(int64(v + 0.5))
		case "sim_queue_depth":
			q.DepthMean = F64(v)
		case "sim_queue_oldest_age_ms":
			q.OldestAgeMs = F64(v)
		case "sim_queue_dlq_count":
			q.DLQCount = I64(int64(v + 0.5))
		}
	// Topic broker (labels: broker_service, topic, partition, consumer_group)
	case "sim_topic_deliver_count", "sim_topic_drop_count", "sim_topic_backlog_depth", "sim_topic_consumer_lag",
		"sim_topic_oldest_age_ms", "sim_topic_dlq_count":
		bs, top := labels["broker_service"], labels["topic"]
		part := 0
		if ps := strings.TrimSpace(labels["partition"]); ps != "" {
			if p, err := strconv.Atoi(ps); err == nil {
				part = p
			}
		}
		cg := labels["consumer_group"]
		if bs == "" || top == "" {
			return fmt.Errorf("%s requires broker_service and topic labels", metric)
		}
		tb := upsertTopicBrokerObs(out, bs, top, part, cg)
		switch m {
		case "sim_topic_deliver_count":
			tb.TopicDeliverCount = I64(int64(v + 0.5))
		case "sim_topic_drop_count":
			tb.DropCount = I64(int64(v + 0.5))
		case "sim_topic_backlog_depth":
			tb.BacklogDepth = F64(v)
		case "sim_topic_consumer_lag":
			tb.ConsumerLag = F64(v)
		case "sim_topic_oldest_age_ms":
			tb.OldestAgeMs = F64(v)
		case "sim_topic_dlq_count":
			tb.DLQCount = I64(int64(v + 0.5))
		}
	default:
		return fmt.Errorf("prometheus_json: unknown metric %q", metric)
	}
	return nil
}

func upsertServiceObs(out *ObservedMetrics, serviceID string, fn func(*ServiceObservation)) {
	for i := range out.Services {
		if out.Services[i].ServiceID == serviceID {
			fn(&out.Services[i])
			return
		}
	}
	var so ServiceObservation
	so.ServiceID = serviceID
	fn(&so)
	out.Services = append(out.Services, so)
}

func upsertEndpointObs(out *ObservedMetrics, svc, ep string) *EndpointObservation {
	for i := range out.Endpoints {
		if out.Endpoints[i].ServiceID == svc && out.Endpoints[i].EndpointPath == ep {
			return &out.Endpoints[i]
		}
	}
	out.Endpoints = append(out.Endpoints, EndpointObservation{ServiceID: svc, EndpointPath: ep})
	return &out.Endpoints[len(out.Endpoints)-1]
}

func upsertQueueBrokerObs(out *ObservedMetrics, broker, topic string) *QueueBrokerObservation {
	for i := range out.QueueBrokers {
		if out.QueueBrokers[i].BrokerService == broker && out.QueueBrokers[i].Topic == topic {
			return &out.QueueBrokers[i]
		}
	}
	out.QueueBrokers = append(out.QueueBrokers, QueueBrokerObservation{BrokerService: broker, Topic: topic})
	return &out.QueueBrokers[len(out.QueueBrokers)-1]
}

func upsertTopicBrokerObs(out *ObservedMetrics, broker, topic string, partition int, cg string) *TopicBrokerObservation {
	for i := range out.TopicBrokers {
		t := &out.TopicBrokers[i]
		if t.BrokerService == broker && t.Topic == topic && t.Partition == partition && t.ConsumerGroup == cg {
			return t
		}
	}
	out.TopicBrokers = append(out.TopicBrokers, TopicBrokerObservation{
		BrokerService: broker, Topic: topic, Partition: partition, ConsumerGroup: cg,
	})
	return &out.TopicBrokers[len(out.TopicBrokers)-1]
}
