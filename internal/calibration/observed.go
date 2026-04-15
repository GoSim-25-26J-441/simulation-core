// Package calibration provides scenario calibration against observed production-like metrics
// and validation of simulator predictions vs observations.
package calibration

import "time"

// ObservationWindow describes the time range and provenance of measurements.
type ObservationWindow struct {
	Duration       time.Duration
	Source         string
	CollectionNote string
}

// EndpointObservation holds per-endpoint aggregates from an external system or golden run.
type EndpointObservation struct {
	ServiceID    string
	EndpointPath string

	ThroughputRPS ObservedValue[float64]

	LatencyP50Ms  ObservedValue[float64]
	LatencyP95Ms  ObservedValue[float64]
	LatencyP99Ms  ObservedValue[float64]
	LatencyMeanMs ObservedValue[float64]

	ProcessingLatencyP50Ms  ObservedValue[float64]
	ProcessingLatencyP95Ms  ObservedValue[float64]
	ProcessingLatencyP99Ms  ObservedValue[float64]
	ProcessingLatencyMeanMs ObservedValue[float64]

	QueueWaitP50Ms  ObservedValue[float64]
	QueueWaitP95Ms  ObservedValue[float64]
	QueueWaitP99Ms  ObservedValue[float64]
	QueueWaitMeanMs ObservedValue[float64]

	DBWaitMeanMs ObservedValue[float64]

	RequestCount ObservedValue[int64]
	ErrorCount   ObservedValue[int64]
	RetryCount   ObservedValue[int64]
	TimeoutCount ObservedValue[int64]
}

// DownstreamEdgeObservation describes an edge (caller → callee) share or volume.
type DownstreamEdgeObservation struct {
	FromService string
	FromPath    string
	To          string // serviceID:path

	SampleCount       ObservedValue[float64]
	ProbabilityHint   ObservedValue[float64]
	CallCountMeanHint ObservedValue[float64]
}

// QueueBrokerObservation is backlog/drop/DLQ for a kind:queue shard.
type QueueBrokerObservation struct {
	BrokerService string
	Topic         string

	DepthMean    ObservedValue[float64]
	DropCount    ObservedValue[int64]
	DLQCount     ObservedValue[int64]
	EnqueueCount ObservedValue[int64]
	DequeueCount ObservedValue[int64]
	// QueuePublishAttemptCount is producer publish attempts (accepted + rejected), matching RunMetrics queue_drop_rate denominator.
	QueuePublishAttemptCount ObservedValue[int64]
	OldestAgeMs ObservedValue[float64]
}

// TopicBrokerObservation is topic consumer-group level observations.
type TopicBrokerObservation struct {
	BrokerService string
	Topic         string
	Partition     int
	ConsumerGroup string

	BacklogDepth ObservedValue[float64]
	ConsumerLag  ObservedValue[float64]
	DropCount    ObservedValue[int64]
	DLQCount     ObservedValue[int64]
	PublishCount ObservedValue[int64]
	// TopicDeliverCount is consumer delivery attempts (subscriber), used with DropCount for topic_drop_rate = drop / (deliver + drop).
	TopicDeliverCount ObservedValue[int64]
	OldestAgeMs       ObservedValue[float64]
}

// CacheObservation hit/miss counts for cache-style services.
type CacheObservation struct {
	ServiceID string
	HitCount  ObservedValue[int64]
	MissCount ObservedValue[int64]
}

// GlobalObservation aggregates run-wide SLO-oriented metrics.
type GlobalObservation struct {
	RootLatencyP50Ms  ObservedValue[float64]
	RootLatencyP95Ms  ObservedValue[float64]
	RootLatencyP99Ms  ObservedValue[float64]
	RootLatencyMeanMs ObservedValue[float64]

	IngressThroughputRPS ObservedValue[float64]
	IngressErrorRate       ObservedValue[float64]

	TotalRequests         ObservedValue[int64]
	IngressRequests       ObservedValue[int64]
	FailedRequests        ObservedValue[int64]
	RetryAttempts         ObservedValue[int64]
	TimeoutErrors         ObservedValue[int64]
	IngressFailedRequests ObservedValue[int64]
}

// WorkloadTargetObservation optionally pins ingress scaling to a workload pattern.
type WorkloadTargetObservation struct {
	To           string
	TrafficClass string
	SourceKind   string

	ThroughputRPS ObservedValue[float64]
}

// ServiceObservation rolls up utilization for a service.
type ServiceObservation struct {
	ServiceID         string
	CPUUtilization    ObservedValue[float64]
	MemoryUtilization ObservedValue[float64]
}

// ObservedMetrics is vendor-neutral input for calibration and validation.
// Optional fields use ObservedValue so explicit zeros are not confused with "not observed".
type ObservedMetrics struct {
	Window ObservationWindow

	Global GlobalObservation

	Endpoints       []EndpointObservation
	Services        []ServiceObservation
	DownstreamEdges []DownstreamEdgeObservation
	QueueBrokers    []QueueBrokerObservation
	TopicBrokers    []TopicBrokerObservation
	Caches          []CacheObservation

	// WorkloadTargets optionally supply throughput per workload pattern (To / traffic_class / source_kind).
	WorkloadTargets []WorkloadTargetObservation
}
