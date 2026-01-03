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
	CPUUtilization     float64                    `json:"cpu_utilization"`
	MemoryUtilization  float64                    `json:"memory_utilization"`
	ServiceMetrics     map[string]*ServiceMetrics `json:"service_metrics,omitempty"`
}

// ServiceMetrics contains metrics for a specific service
type ServiceMetrics struct {
	ServiceName       string  `json:"service_name"`
	RequestCount      int64   `json:"request_count"`
	ErrorCount        int64   `json:"error_count"`
	LatencyP50        float64 `json:"latency_p50_ms"`
	LatencyP95        float64 `json:"latency_p95_ms"`
	LatencyP99        float64 `json:"latency_p99_ms"`
	LatencyMean       float64 `json:"latency_mean_ms"`
	CPUUtilization    float64 `json:"cpu_utilization"`
	MemoryUtilization float64 `json:"memory_utilization"`
	ActiveReplicas    int     `json:"active_replicas"`
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
