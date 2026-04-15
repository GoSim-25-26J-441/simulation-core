package calibration

import (
	"math"
	"testing"
)

type adapterMatrixExpect struct {
	windowSeconds float64

	ingressThroughput float64
	rootLatencyP95    float64
	retryAttempts     int64
	timeoutErrors     int64

	serviceID   string
	cpuUtil     float64
	memoryUtil  float64
	hasService  bool

	endpointService string
	endpointPath    string
	latencyP95      float64
	queueWaitMean   float64
	processingMean  float64
	requestCount    int64
	errorCount      int64

	queueBroker string
	queueTopic  string
	queueDepth  float64
	hasQueue    bool

	topicBroker string
	topicName   string
	partition   int
	consumerGrp string
	topicLag    float64
	topicBacklog float64
	hasTopic    bool
}

func assertAdapterMatrix(t *testing.T, obs *ObservedMetrics, exp adapterMatrixExpect) {
	t.Helper()
	if obs == nil {
		t.Fatal("obs is nil")
	}
	if exp.windowSeconds > 0 {
		gotSec := obs.Window.Duration.Seconds()
		if math.Abs(gotSec-exp.windowSeconds) > 1e-9 {
			t.Fatalf("window seconds mismatch: got=%v want=%v", gotSec, exp.windowSeconds)
		}
	}

	assertPresentFloatEqual(t, "global.ingress_throughput_rps", obs.Global.IngressThroughputRPS, F64(exp.ingressThroughput), 1e-9)
	assertPresentFloatEqual(t, "global.root_latency_p95_ms", obs.Global.RootLatencyP95Ms, F64(exp.rootLatencyP95), 1e-9)
	assertPresentIntEqual(t, "global.retry_attempts", obs.Global.RetryAttempts, I64(exp.retryAttempts))
	assertPresentIntEqual(t, "global.timeout_errors", obs.Global.TimeoutErrors, I64(exp.timeoutErrors))

	ep := findEndpointObs(t, obs, exp.endpointService, exp.endpointPath)
	assertPresentFloatEqual(t, "endpoint.latency_p95_ms", ep.LatencyP95Ms, F64(exp.latencyP95), 1e-9)
	assertPresentFloatEqual(t, "endpoint.queue_wait_mean_ms", ep.QueueWaitMeanMs, F64(exp.queueWaitMean), 1e-9)
	assertPresentFloatEqual(t, "endpoint.processing_latency_mean_ms", ep.ProcessingLatencyMeanMs, F64(exp.processingMean), 1e-9)
	assertPresentIntEqual(t, "endpoint.request_count", ep.RequestCount, I64(exp.requestCount))
	assertPresentIntEqual(t, "endpoint.error_count", ep.ErrorCount, I64(exp.errorCount))

	if exp.hasService {
		svc := findServiceObs(t, obs, exp.serviceID)
		assertPresentFloatEqual(t, "service.cpu_utilization", svc.CPUUtilization, F64(exp.cpuUtil), 1e-9)
		assertPresentFloatEqual(t, "service.memory_utilization", svc.MemoryUtilization, F64(exp.memoryUtil), 1e-9)
	}
	if exp.hasQueue {
		q := findQueueObs(t, obs, exp.queueBroker, exp.queueTopic)
		assertPresentFloatEqual(t, "queue.depth_mean", q.DepthMean, F64(exp.queueDepth), 1e-9)
	}
	if exp.hasTopic {
		tb := findTopicObs(t, obs, exp.topicBroker, exp.topicName, exp.partition, exp.consumerGrp)
		assertPresentFloatEqual(t, "topic.consumer_lag", tb.ConsumerLag, F64(exp.topicLag), 1e-9)
		assertPresentFloatEqual(t, "topic.backlog_depth", tb.BacklogDepth, F64(exp.topicBacklog), 1e-9)
	}
}

func assertPresentFloatEqual(t *testing.T, name string, a, b ObservedValue[float64], eps float64) {
	t.Helper()
	if !a.Present || !b.Present {
		t.Fatalf("%s presence mismatch: a=%+v b=%+v", name, a, b)
	}
	if math.Abs(a.Value-b.Value) > eps {
		t.Fatalf("%s value mismatch: %v vs %v", name, a.Value, b.Value)
	}
}

func assertPresentIntEqual(t *testing.T, name string, a, b ObservedValue[int64]) {
	t.Helper()
	if !a.Present || !b.Present {
		t.Fatalf("%s presence mismatch: a=%+v b=%+v", name, a, b)
	}
	if a.Value != b.Value {
		t.Fatalf("%s value mismatch: %d vs %d", name, a.Value, b.Value)
	}
}

func findEndpointObs(t *testing.T, obs *ObservedMetrics, service, path string) EndpointObservation {
	t.Helper()
	for _, e := range obs.Endpoints {
		if e.ServiceID == service && e.EndpointPath == path {
			return e
		}
	}
	t.Fatalf("endpoint %s:%s not found in %+v", service, path, obs.Endpoints)
	return EndpointObservation{}
}

func findServiceObs(t *testing.T, obs *ObservedMetrics, service string) ServiceObservation {
	t.Helper()
	for _, s := range obs.Services {
		if s.ServiceID == service {
			return s
		}
	}
	t.Fatalf("service %s not found in %+v", service, obs.Services)
	return ServiceObservation{}
}

func findQueueObs(t *testing.T, obs *ObservedMetrics, broker, topic string) QueueBrokerObservation {
	t.Helper()
	for _, q := range obs.QueueBrokers {
		if q.BrokerService == broker && q.Topic == topic {
			return q
		}
	}
	t.Fatalf("queue broker %s:%s not found in %+v", broker, topic, obs.QueueBrokers)
	return QueueBrokerObservation{}
}

func findTopicObs(t *testing.T, obs *ObservedMetrics, broker, topic string, partition int, cg string) TopicBrokerObservation {
	t.Helper()
	for _, tb := range obs.TopicBrokers {
		if tb.BrokerService == broker && tb.Topic == topic && tb.Partition == partition && tb.ConsumerGroup == cg {
			return tb
		}
	}
	t.Fatalf("topic broker %s:%s:%d:%s not found in %+v", broker, topic, partition, cg, obs.TopicBrokers)
	return TopicBrokerObservation{}
}

func observedMatrixExpectation() adapterMatrixExpect {
	return adapterMatrixExpect{
		windowSeconds: 30,

		ingressThroughput: 12,
		rootLatencyP95:    40,
		retryAttempts:     2,
		timeoutErrors:     1,

		serviceID:  "api",
		cpuUtil:    0.42,
		memoryUtil: 0.55,
		hasService: true,

		endpointService: "api",
		endpointPath:    "/a",
		latencyP95:      40,
		queueWaitMean:   4,
		processingMean:  8,
		requestCount:    100,
		errorCount:      3,

		queueBroker: "q",
		queueTopic:  "/orders",
		queueDepth:  3,
		hasQueue:    true,

		topicBroker: "t",
		topicName:   "/ev",
		partition:   2,
		consumerGrp: "g1",
		topicLag:    7,
		topicBacklog: 9,
		hasTopic:    true,
	}
}

