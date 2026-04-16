package models

import (
	"sync"
	"time"
)

// RunStatus represents the status of a simulation run
type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

// Run represents a simulation run
type Run struct {
	ID        string                 `json:"id"`
	Status    RunStatus              `json:"status"`
	Config    map[string]interface{} `json:"config"`
	StartTime time.Time              `json:"start_time"`
	EndTime   time.Time              `json:"end_time,omitempty"`
	Duration  time.Duration          `json:"duration,omitempty"`
	Metrics   *RunMetrics            `json:"metrics,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Metadata  map[string]string      `json:"metadata,omitempty"`
}

// RunMetrics contains aggregated metrics for a simulation run
type RunMetrics struct {
	TotalRequests      int64                      `json:"total_requests"`
	SuccessfulRequests int64                      `json:"successful_requests"`
	FailedRequests     int64                      `json:"failed_requests"`
	LatencyP50         float64                    `json:"latency_p50_ms"`
	LatencyP95         float64                    `json:"latency_p95_ms"`
	LatencyP99         float64                    `json:"latency_p99_ms"`
	LatencyMean        float64                    `json:"latency_mean_ms"`
	ThroughputRPS      float64                    `json:"throughput_rps"`
	// IngressRequests counts workload arrivals (origin=ingress). InternalRequests counts downstream hops.
	IngressRequests  int64   `json:"ingress_requests,omitempty"`
	InternalRequests int64   `json:"internal_requests,omitempty"`
	// IngressThroughputRPS is ingress RPS (SLOs, batch guardrails). ThroughputRPS remains aggregate work over all hops.
	IngressThroughputRPS float64 `json:"ingress_throughput_rps,omitempty"`
	// IngressFailedRequests counts user-visible root/ingress logical failures (one per failed external trace).
	IngressFailedRequests int64 `json:"ingress_failed_requests,omitempty"`
	// IngressErrorRate is ingress_failed_requests / ingress_requests when ingress_requests > 0.
	IngressErrorRate float64 `json:"ingress_error_rate,omitempty"`
	// AttemptFailedRequests is the sum of request_error_count samples (attempt-level, includes retries).
	AttemptFailedRequests int64 `json:"attempt_failed_requests,omitempty"`
	// AttemptErrorRate is attempt_failed_requests / total_requests when total_requests > 0.
	AttemptErrorRate float64 `json:"attempt_error_rate,omitempty"`
	RetryAttempts    int64   `json:"retry_attempts,omitempty"`
	TimeoutErrors    int64   `json:"timeout_errors,omitempty"`
	// Broker queue rollups (counters sum all label series; queue_depth_sum sums latest gauge per label set).
	QueueEnqueueCountTotal    int64   `json:"queue_enqueue_count_total,omitempty"`
	QueueDequeueCountTotal      int64   `json:"queue_dequeue_count_total,omitempty"`
	QueueDropCountTotal         int64   `json:"queue_drop_count_total,omitempty"`
	QueueRedeliveryCountTotal   int64   `json:"queue_redelivery_count_total,omitempty"`
	QueueDlqCountTotal          int64   `json:"queue_dlq_count_total,omitempty"`
	QueueDepthSum               float64 `json:"queue_depth_sum,omitempty"`
	// Topic / pub-sub broker rollups (counters sum all label series; *_depth_sum sums latest gauge per label set).
	TopicPublishCountTotal     int64   `json:"topic_publish_count_total,omitempty"`
	TopicDeliverCountTotal     int64   `json:"topic_deliver_count_total,omitempty"`
	TopicDropCountTotal        int64   `json:"topic_drop_count_total,omitempty"`
	TopicRedeliveryCountTotal  int64   `json:"topic_redelivery_count_total,omitempty"`
	TopicDlqCountTotal         int64   `json:"topic_dlq_count_total,omitempty"`
	TopicBacklogDepthSum       float64 `json:"topic_backlog_depth_sum,omitempty"`
	TopicConsumerLagSum        float64 `json:"topic_consumer_lag_sum,omitempty"`
	QueueOldestMessageAgeMs    float64 `json:"queue_oldest_message_age_ms,omitempty"`
	TopicOldestMessageAgeMs    float64 `json:"topic_oldest_message_age_ms,omitempty"`
	MaxQueueDepth              float64 `json:"max_queue_depth,omitempty"`
	MaxTopicBacklogDepth       float64 `json:"max_topic_backlog_depth,omitempty"`
	MaxTopicConsumerLag        float64 `json:"max_topic_consumer_lag,omitempty"`
	QueueDropRate              float64 `json:"queue_drop_rate,omitempty"`
	TopicDropRate              float64 `json:"topic_drop_rate,omitempty"`
	CPUUtilization   float64 `json:"cpu_utilization"`
	MemoryUtilization  float64                    `json:"memory_utilization"`
	ServiceMetrics     map[string]*ServiceMetrics `json:"service_metrics,omitempty"`
	HostMetrics        map[string]*HostMetrics    `json:"host_metrics,omitempty"`
	// EndpointRequestStats is optional per-endpoint request/error totals when collector labels include service+endpoint.
	EndpointRequestStats []EndpointRequestStats `json:"endpoint_request_stats,omitempty"`
	// InstanceRouteStats is optional per-instance routing selection totals.
	InstanceRouteStats []InstanceRouteStats `json:"instance_route_stats,omitempty"`
	// Topology routing rollups.
	LocalityHitRate               float64 `json:"locality_hit_rate,omitempty"`
	CrossZoneRequestCountTotal    int64   `json:"cross_zone_request_count_total,omitempty"`
	SameZoneRequestCountTotal     int64   `json:"same_zone_request_count_total,omitempty"`
	CrossZoneRequestFraction      float64 `json:"cross_zone_request_fraction,omitempty"`
	// Cross-zone network penalty rollups (from cross_zone_latency_penalty_ms samples on downstream hops).
	CrossZoneLatencyPenaltyMsTotal float64 `json:"cross_zone_latency_penalty_ms_total,omitempty"`
	CrossZoneLatencyPenaltyMsMean  float64 `json:"cross_zone_latency_penalty_ms_mean,omitempty"`
	// Same-zone different-host penalty (from same_zone_latency_penalty_ms).
	SameZoneLatencyPenaltyMsTotal float64 `json:"same_zone_latency_penalty_ms_total,omitempty"`
	SameZoneLatencyPenaltyMsMean  float64 `json:"same_zone_latency_penalty_ms_mean,omitempty"`
	// External-service network overlay (from external_latency_penalty_ms).
	ExternalLatencyMsTotal float64 `json:"external_latency_ms_total,omitempty"`
	ExternalLatencyMsMean  float64 `json:"external_latency_ms_mean,omitempty"`
	// Aggregate topology penalty across all network classes (from topology_latency_penalty_ms).
	TopologyLatencyPenaltyMsTotal float64 `json:"topology_latency_penalty_ms_total,omitempty"`
	TopologyLatencyPenaltyMsMean  float64 `json:"topology_latency_penalty_ms_mean,omitempty"`
}

// EndpointRequestStats aggregates ingress/hop request and error counts for one endpoint (from collector labels).
// Optional latency and queue/processing rollups use pointers so JSON omits unset fields (distinct from explicit zero).
type EndpointRequestStats struct {
	ServiceName  string `json:"service_name"`
	EndpointPath string `json:"endpoint_path"`
	RequestCount int64  `json:"request_count"`
	ErrorCount   int64  `json:"error_count"`
	// Hop / service-request latency (service_request_latency_ms or request_latency_ms for this service+endpoint).
	LatencyP50Ms  *float64 `json:"latency_p50_ms,omitempty"`
	LatencyP95Ms  *float64 `json:"latency_p95_ms,omitempty"`
	LatencyP99Ms  *float64 `json:"latency_p99_ms,omitempty"`
	LatencyMeanMs *float64 `json:"latency_mean_ms,omitempty"`
	// RootRequestLatency for ingress roots only (same label subset when emitted).
	RootLatencyP50Ms  *float64 `json:"root_latency_p50_ms,omitempty"`
	RootLatencyP95Ms  *float64 `json:"root_latency_p95_ms,omitempty"`
	RootLatencyP99Ms  *float64 `json:"root_latency_p99_ms,omitempty"`
	RootLatencyMeanMs *float64 `json:"root_latency_mean_ms,omitempty"`
	QueueWaitP50Ms  *float64 `json:"queue_wait_p50_ms,omitempty"`
	QueueWaitP95Ms  *float64 `json:"queue_wait_p95_ms,omitempty"`
	QueueWaitP99Ms  *float64 `json:"queue_wait_p99_ms,omitempty"`
	QueueWaitMeanMs *float64 `json:"queue_wait_mean_ms,omitempty"`
	ProcessingLatencyP50Ms  *float64 `json:"processing_latency_p50_ms,omitempty"`
	ProcessingLatencyP95Ms  *float64 `json:"processing_latency_p95_ms,omitempty"`
	ProcessingLatencyP99Ms  *float64 `json:"processing_latency_p99_ms,omitempty"`
	ProcessingLatencyMeanMs *float64 `json:"processing_latency_mean_ms,omitempty"`
}

// InstanceRouteStats aggregates routing selections for one service+endpoint+instance tuple.
type InstanceRouteStats struct {
	ServiceName    string `json:"service_name"`
	EndpointPath   string `json:"endpoint_path"`
	InstanceID     string `json:"instance_id"`
	Strategy       string `json:"strategy,omitempty"`
	SelectionCount int64  `json:"selection_count"`
}

// HostMetrics holds utilization observed on a host (when the simulator records host-level gauges).
type HostMetrics struct {
	HostID              string  `json:"host_id"`
	CPUUtilization      float64 `json:"cpu_utilization"`
	MemoryUtilization   float64 `json:"memory_utilization"`
}

// ServiceMetrics contains metrics for a specific service
type ServiceMetrics struct {
	ServiceName        string  `json:"service_name"`
	RequestCount       int64   `json:"request_count"`
	ErrorCount         int64   `json:"error_count"`
	LatencyP50         float64 `json:"latency_p50_ms"`
	LatencyP95         float64 `json:"latency_p95_ms"`
	LatencyP99         float64 `json:"latency_p99_ms"`
	LatencyMean        float64 `json:"latency_mean_ms"`
	CPUUtilization     float64 `json:"cpu_utilization"`
	MemoryUtilization  float64 `json:"memory_utilization"`
	ActiveReplicas     int     `json:"active_replicas"`
	ConcurrentRequests int     `json:"concurrent_requests"`
	// QueueLength is the sum of the latest queue_length gauge per instance (current state).
	QueueLength int `json:"queue_length"`
	// Queue wait (DES ArrivalTime → StartTime) aggregates for this service (all endpoints).
	QueueWaitP50Ms float64 `json:"queue_wait_p50_ms,omitempty"`
	QueueWaitP95Ms float64 `json:"queue_wait_p95_ms,omitempty"`
	QueueWaitP99Ms float64 `json:"queue_wait_p99_ms,omitempty"`
	QueueWaitMeanMs float64 `json:"queue_wait_mean_ms,omitempty"`
	// Processing latency (StartTime → completion, CPU + net for the hop) aggregates.
	ProcessingLatencyP50Ms  float64 `json:"processing_latency_p50_ms,omitempty"`
	ProcessingLatencyP95Ms  float64 `json:"processing_latency_p95_ms,omitempty"`
	ProcessingLatencyP99Ms  float64 `json:"processing_latency_p99_ms,omitempty"`
	ProcessingLatencyMeanMs float64 `json:"processing_latency_mean_ms,omitempty"`
}

// RequestStatus represents the status of a request
type RequestStatus string

const (
	RequestStatusPending    RequestStatus = "pending"
	RequestStatusProcessing RequestStatus = "processing"
	RequestStatusCompleted  RequestStatus = "completed"
	RequestStatusFailed     RequestStatus = "failed"
)

// Request represents a request in the simulation
type Request struct {
	ID               string                 `json:"id"`
	TraceID          string                 `json:"trace_id"`
	ParentID         string                 `json:"parent_id,omitempty"`
	ServiceName      string                 `json:"service_name"`
	Endpoint         string                 `json:"endpoint"`
	Status           RequestStatus          `json:"status"`
	ArrivalTime      time.Time              `json:"arrival_time"`
	StartTime        time.Time              `json:"start_time,omitempty"`
	CompletionTime   time.Time              `json:"completion_time,omitempty"`
	Duration         time.Duration          `json:"duration,omitempty"`
	CPUTimeMs        float64                `json:"cpu_time_ms"`
	NetworkLatencyMs float64                `json:"network_latency_ms"`
	QueueTimeMs      float64                `json:"queue_time_ms"`
	Error            string                 `json:"error,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

// Trace represents a complete trace of a request through the system
type Trace struct {
	ID             string        `json:"id"`
	RootRequestID  string        `json:"root_request_id"`
	StartTime      time.Time     `json:"start_time"`
	EndTime        time.Time     `json:"end_time,omitempty"`
	Duration       time.Duration `json:"duration,omitempty"`
	Requests       []*Request    `json:"requests"`
	TotalLatencyMs float64       `json:"total_latency_ms"`
	Success        bool          `json:"success"`
	mu             sync.RWMutex
}

// AddRequest adds a request to the trace (thread-safe)
func (t *Trace) AddRequest(req *Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Requests = append(t.Requests, req)
}

// GetRequests returns all requests in the trace (thread-safe)
func (t *Trace) GetRequests() []*Request {
	t.mu.RLock()
	defer t.mu.RUnlock()
	requests := make([]*Request, len(t.Requests))
	copy(requests, t.Requests)
	return requests
}

// ServiceInstance represents a running service instance
type ServiceInstance struct {
	ID             string    `json:"id"`
	ServiceName    string    `json:"service_name"`
	HostID         string    `json:"host_id"`
	ClusterName    string    `json:"cluster_name"`
	Status         string    `json:"status"` // running, stopped, failed
	CPUCores       float64   `json:"cpu_cores"`
	MemoryMB       float64   `json:"memory_mb"`
	CPUUsage       float64   `json:"cpu_usage"`    // 0.0 to 1.0
	MemoryUsage    float64   `json:"memory_usage"` // 0.0 to 1.0
	ActiveRequests int       `json:"active_requests"`
	RequestQueue   []string  `json:"request_queue"` // Request IDs
	StartTime      time.Time `json:"start_time"`
	mu             sync.RWMutex
}

// IncrementActiveRequests increments the active request counter (thread-safe)
func (s *ServiceInstance) IncrementActiveRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ActiveRequests++
}

// DecrementActiveRequests decrements the active request counter (thread-safe)
func (s *ServiceInstance) DecrementActiveRequests() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ActiveRequests > 0 {
		s.ActiveRequests--
	}
}

// GetActiveRequests returns the number of active requests (thread-safe)
func (s *ServiceInstance) GetActiveRequests() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ActiveRequests
}

// EnqueueRequest adds a request to the queue (thread-safe)
func (s *ServiceInstance) EnqueueRequest(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.RequestQueue = append(s.RequestQueue, requestID)
}

// DequeueRequest removes and returns the next request from the queue (thread-safe)
func (s *ServiceInstance) DequeueRequest() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.RequestQueue) == 0 {
		return "", false
	}
	requestID := s.RequestQueue[0]
	s.RequestQueue = s.RequestQueue[1:]
	return requestID, true
}

// QueueLength returns the current queue length (thread-safe)
func (s *ServiceInstance) QueueLength() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.RequestQueue)
}

// Host represents a physical or virtual host
type Host struct {
	ID                string   `json:"id"`
	ClusterName       string   `json:"cluster_name"`
	CPUCores          int      `json:"cpu_cores"`
	MemoryGB          int      `json:"memory_gb"`
	CPUUtilization    float64  `json:"cpu_utilization"`    // 0.0 to 1.0
	MemoryUtilization float64  `json:"memory_utilization"` // 0.0 to 1.0
	Services          []string `json:"services"`           // Service instance IDs
	mu                sync.RWMutex
}

// AddService adds a service to the host (thread-safe)
func (h *Host) AddService(serviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Services = append(h.Services, serviceID)
}

// RemoveService removes a service from the host (thread-safe)
func (h *Host) RemoveService(serviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, id := range h.Services {
		if id == serviceID {
			h.Services = append(h.Services[:i], h.Services[i+1:]...)
			break
		}
	}
}

// GetServices returns all services on the host (thread-safe)
func (h *Host) GetServices() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	services := make([]string, len(h.Services))
	copy(services, h.Services)
	return services
}

// MetricPoint represents a single metric data point
type MetricPoint struct {
	Timestamp time.Time         `json:"timestamp"`
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// MetricsSummary represents a summary of collected metrics
type MetricsSummary struct {
	StartTime    time.Time               `json:"start_time"`
	EndTime      time.Time               `json:"end_time"`
	Duration     time.Duration           `json:"duration"`
	Metrics      map[string][]float64    `json:"metrics"` // metric name -> values
	Aggregations map[string]*Aggregation `json:"aggregations,omitempty"`
}

// Aggregation represents aggregated statistics for a metric
type Aggregation struct {
	Count int64   `json:"count"`
	Sum   float64 `json:"sum"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
}
