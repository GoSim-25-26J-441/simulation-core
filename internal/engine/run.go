package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

// RunManager manages the lifecycle of a simulation run
type RunManager struct {
	run            *models.Run
	traces         map[string]*models.Trace
	requests       map[string]*models.Request
	serviceMetrics map[string]*models.ServiceMetrics
	latencies      []float64
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
}

// NewRunManager creates a new run manager
func NewRunManager(runID string) *RunManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &RunManager{
		run: &models.Run{
			ID:        runID,
			Status:    models.RunStatusPending,
			StartTime: time.Now(),
			Config:    make(map[string]interface{}),
			Metadata:  make(map[string]string),
		},
		traces:         make(map[string]*models.Trace),
		requests:       make(map[string]*models.Request),
		serviceMetrics: make(map[string]*models.ServiceMetrics),
		latencies:      make([]float64, 0),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Start marks the run as started
func (rm *RunManager) Start() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.run.Status = models.RunStatusRunning
	rm.run.StartTime = time.Now()
}

// Complete marks the run as completed
func (rm *RunManager) Complete() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.run.Status = models.RunStatusCompleted
	rm.run.EndTime = time.Now()
	rm.run.Duration = rm.run.EndTime.Sub(rm.run.StartTime)

	// Calculate final metrics
	rm.run.Metrics = rm.calculateMetrics()
}

// Fail marks the run as failed
func (rm *RunManager) Fail(err error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.run.Status = models.RunStatusFailed
	rm.run.EndTime = time.Now()
	rm.run.Duration = rm.run.EndTime.Sub(rm.run.StartTime)
	rm.run.Error = err.Error()
}

// Cancel cancels the run
func (rm *RunManager) Cancel() {
	rm.cancel()
}

// Context returns the run's context
func (rm *RunManager) Context() context.Context {
	return rm.ctx
}

// GetRun returns the current run state (thread-safe)
func (rm *RunManager) GetRun() *models.Run {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	// Create a copy to avoid race conditions
	runCopy := *rm.run
	return &runCopy
}

// AddTrace adds a trace to the run
func (rm *RunManager) AddTrace(trace *models.Trace) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.traces[trace.ID] = trace
}

// GetTrace retrieves a trace by ID
func (rm *RunManager) GetTrace(traceID string) (*models.Trace, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	trace, ok := rm.traces[traceID]
	return trace, ok
}

// AddRequest adds a request to the run
func (rm *RunManager) AddRequest(request *models.Request) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.requests[request.ID] = request
}

// GetRequest retrieves a request by ID
func (rm *RunManager) GetRequest(requestID string) (*models.Request, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	request, ok := rm.requests[requestID]
	return request, ok
}

// RecordLatency records a request latency
func (rm *RunManager) RecordLatency(latencyMs float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.latencies = append(rm.latencies, latencyMs)
}

// UpdateServiceMetrics updates metrics for a service
func (rm *RunManager) UpdateServiceMetrics(serviceName string, metrics *models.ServiceMetrics) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.serviceMetrics[serviceName] = metrics
}

// GetServiceMetrics retrieves metrics for a service
func (rm *RunManager) GetServiceMetrics(serviceName string) (*models.ServiceMetrics, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	metrics, ok := rm.serviceMetrics[serviceName]
	return metrics, ok
}

// SetConfig sets a configuration value
func (rm *RunManager) SetConfig(key string, value interface{}) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.run.Config[key] = value
}

// GetConfig gets a configuration value
func (rm *RunManager) GetConfig(key string) (interface{}, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	value, ok := rm.run.Config[key]
	return value, ok
}

// SetMetadata sets a metadata value
func (rm *RunManager) SetMetadata(key, value string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.run.Metadata[key] = value
}

// GetMetadata gets a metadata value
func (rm *RunManager) GetMetadata(key string) (string, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	value, ok := rm.run.Metadata[key]
	return value, ok
}

// calculateMetrics calculates final run metrics
func (rm *RunManager) calculateMetrics() *models.RunMetrics {
	totalRequests := int64(len(rm.requests))
	successfulRequests := int64(0)
	failedRequests := int64(0)

	for _, req := range rm.requests {
		if req.Status == models.RequestStatusCompleted && req.Error == "" {
			successfulRequests++
		} else if req.Status == models.RequestStatusFailed || req.Error != "" {
			failedRequests++
		}
	}

	var latencyP50, latencyP95, latencyP99, latencyMean float64
	if len(rm.latencies) > 0 {
		latencyP50 = utils.P50(rm.latencies)
		latencyP95 = utils.P95(rm.latencies)
		latencyP99 = utils.P99(rm.latencies)
		latencyMean = utils.Mean(rm.latencies)
	}

	duration := rm.run.EndTime.Sub(rm.run.StartTime)
	throughputRPS := float64(totalRequests) / duration.Seconds()

	return &models.RunMetrics{
		TotalRequests:      totalRequests,
		SuccessfulRequests: successfulRequests,
		FailedRequests:     failedRequests,
		LatencyP50:         latencyP50,
		LatencyP95:         latencyP95,
		LatencyP99:         latencyP99,
		LatencyMean:        latencyMean,
		ThroughputRPS:      throughputRPS,
		ServiceMetrics:     rm.serviceMetrics,
	}
}

// GetStats returns current run statistics
func (rm *RunManager) GetStats() map[string]interface{} {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	totalRequests := len(rm.requests)
	totalTraces := len(rm.traces)
	completedRequests := 0
	failedRequests := 0

	for _, req := range rm.requests {
		if req.Status == models.RequestStatusCompleted {
			completedRequests++
		} else if req.Status == models.RequestStatusFailed {
			failedRequests++
		}
	}

	var currentLatencyP50, currentLatencyP95 float64
	if len(rm.latencies) > 0 {
		currentLatencyP50 = utils.P50(rm.latencies)
		currentLatencyP95 = utils.P95(rm.latencies)
	}

	elapsed := time.Since(rm.run.StartTime)

	return map[string]interface{}{
		"status":             rm.run.Status,
		"elapsed":            elapsed.String(),
		"total_requests":     totalRequests,
		"completed_requests": completedRequests,
		"failed_requests":    failedRequests,
		"total_traces":       totalTraces,
		"current_p50_ms":     fmt.Sprintf("%.2f", currentLatencyP50),
		"current_p95_ms":     fmt.Sprintf("%.2f", currentLatencyP95),
		"throughput_rps":     fmt.Sprintf("%.2f", float64(totalRequests)/elapsed.Seconds()),
	}
}
