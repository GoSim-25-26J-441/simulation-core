package engine

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/utils"
)

// RunManager manages the lifecycle of a simulation run
type RunManager struct {
	run                  *models.Run
	traces               map[string]*models.Trace
	requests             map[string]*models.Request
	completedRequests    map[string]*models.Request
	completedOrder       []string
	serviceMetrics       map[string]*models.ServiceMetrics
	latencySummary       latencySummary
	totalRequests        int64
	completedCount       int64
	failedCount          int64
	maxActiveRequests    int
	maxTotalRequests     int
	maxCompletedKeep     int
	traceSamplingRate    float64
	onActiveLimitReached func(currentCount, max int)
	onTotalLimitReached  func(currentCount, max int)
	mu                   sync.RWMutex
	ctx                  context.Context
	cancel               context.CancelFunc
}

const defaultLatencyReservoirSize = 2048

type latencySummary struct {
	count      int64
	sum        float64
	min        float64
	max        float64
	mean       float64
	reservoir  []float64
	maxResSize int
	seenValues int64
	dirty      bool
	cachedP50  float64
	cachedP95  float64
	cachedP99  float64
}

type latencySnapshot struct {
	Count int64
	Mean  float64
	P50   float64
	P95   float64
	P99   float64
}

func loadPositiveIntEnv(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func newLatencySummary(maxReservoir int) latencySummary {
	if maxReservoir <= 0 {
		maxReservoir = defaultLatencyReservoirSize
	}
	return latencySummary{
		min:        math.Inf(1),
		max:        math.Inf(-1),
		maxResSize: maxReservoir,
		reservoir:  make([]float64, 0, maxReservoir),
	}
}

func (s *latencySummary) record(value float64) {
	s.count++
	s.sum += value
	if value < s.min {
		s.min = value
	}
	if value > s.max {
		s.max = value
	}
	s.mean = s.sum / float64(s.count)
	s.seenValues++
	if len(s.reservoir) < s.maxResSize {
		s.reservoir = append(s.reservoir, value)
	} else if s.maxResSize > 0 {
		idx := int((s.seenValues * 1103515245) % int64(s.maxResSize))
		if idx >= 0 && idx < len(s.reservoir) {
			s.reservoir[idx] = value
		}
	}
	s.dirty = true
}

func (s *latencySummary) ensurePercentiles() {
	if !s.dirty {
		return
	}
	if len(s.reservoir) == 0 {
		s.cachedP50 = 0
		s.cachedP95 = 0
		s.cachedP99 = 0
		s.dirty = false
		return
	}
	vals := append([]float64(nil), s.reservoir...)
	sort.Float64s(vals)
	s.cachedP50 = utils.P50(vals)
	s.cachedP95 = utils.P95(vals)
	s.cachedP99 = utils.P99(vals)
	s.dirty = false
}

func (s *latencySummary) snapshot() latencySnapshot {
	if s.count == 0 {
		return latencySnapshot{}
	}
	s.ensurePercentiles()
	return latencySnapshot{
		Count: s.count,
		Mean:  s.mean,
		P50:   s.cachedP50,
		P95:   s.cachedP95,
		P99:   s.cachedP99,
	}
}

type RunManagerSnapshot struct {
	ActiveRequests           int   `json:"active_requests"`
	TotalRequests            int64 `json:"total_requests"`
	CompletedRequests        int64 `json:"completed_requests"`
	FailedRequests           int64 `json:"failed_requests"`
	RetainedCompletedSamples int   `json:"retained_completed_samples"`
}

// NewRunManager creates a new run manager
func NewRunManager(runID string) *RunManager {
	ctx, cancel := context.WithCancel(context.Background())

	maxCompletedKeep := 1000
	if s := os.Getenv("SIMD_MAX_COMPLETED_REQUEST_TRACES"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			maxCompletedKeep = n
		}
	}
	traceSamplingRate := 1.0
	if s := os.Getenv("SIMD_REQUEST_TRACE_SAMPLING_RATE"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 && v <= 1 {
			traceSamplingRate = v
		}
	}
	latencyReservoirSize := loadPositiveIntEnv("SIMD_RUN_LATENCY_RESERVOIR_SIZE", defaultLatencyReservoirSize)
	return &RunManager{
		run: &models.Run{
			ID:        runID,
			Status:    models.RunStatusPending,
			StartTime: time.Now(),
			Config:    make(map[string]interface{}),
			Metadata:  make(map[string]string),
		},
		traces:            make(map[string]*models.Trace),
		requests:          make(map[string]*models.Request),
		completedRequests: make(map[string]*models.Request),
		completedOrder:    make([]string, 0, maxCompletedKeep),
		serviceMetrics:    make(map[string]*models.ServiceMetrics),
		latencySummary:    newLatencySummary(latencyReservoirSize),
		maxCompletedKeep:  maxCompletedKeep,
		traceSamplingRate: traceSamplingRate,
		ctx:               ctx,
		cancel:            cancel,
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
	if rm.maxTotalRequests > 0 && int(rm.totalRequests) >= rm.maxTotalRequests {
		if rm.onTotalLimitReached != nil {
			rm.onTotalLimitReached(int(rm.totalRequests)+1, rm.maxTotalRequests)
		}
		return
	}
	if rm.maxActiveRequests > 0 && len(rm.requests) >= rm.maxActiveRequests {
		if rm.onActiveLimitReached != nil {
			rm.onActiveLimitReached(len(rm.requests)+1, rm.maxActiveRequests)
		}
		return
	}
	rm.requests[request.ID] = request
	rm.totalRequests++
}

// SetMaxRequestsTracked configures an optional hard cap for tracked requests.
// When the cap is reached, additional requests are dropped and onLimitReached is invoked.
func (rm *RunManager) SetMaxRequestsTracked(max int, onLimitReached func(currentCount, max int)) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.maxActiveRequests = max
	rm.onActiveLimitReached = onLimitReached
}

// SetMaxTotalRequests sets an optional cap on total requests created over the run lifetime.
func (rm *RunManager) SetMaxTotalRequests(max int, onLimitReached func(currentCount, max int)) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.maxTotalRequests = max
	rm.onTotalLimitReached = onLimitReached
}

// GetRequest retrieves a request by ID
func (rm *RunManager) GetRequest(requestID string) (*models.Request, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	request, ok := rm.requests[requestID]
	if ok {
		return request, ok
	}
	request, ok = rm.completedRequests[requestID]
	return request, ok
}

// ListRequests returns a snapshot of all requests (for tests and diagnostics).
func (rm *RunManager) ListRequests() []*models.Request {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	out := make([]*models.Request, 0, len(rm.requests)+len(rm.completedRequests))
	for _, r := range rm.requests {
		out = append(out, r)
	}
	for _, id := range rm.completedOrder {
		if r, ok := rm.completedRequests[id]; ok {
			out = append(out, r)
		}
	}
	return out
}

// FinalizeRequest moves a terminal request out of active state into bounded completed samples.
func (rm *RunManager) FinalizeRequest(request *models.Request) {
	if request == nil {
		return
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	delete(rm.requests, request.ID)
	rm.completedCount++
	if request.Status == models.RequestStatusFailed || request.Error != "" {
		rm.failedCount++
	}
	if !rm.shouldSampleCompletedRequest(request.ID) {
		return
	}
	rm.completedRequests[request.ID] = request
	rm.completedOrder = append(rm.completedOrder, request.ID)
	for len(rm.completedOrder) > rm.maxCompletedKeep {
		evictID := rm.completedOrder[0]
		rm.completedOrder = rm.completedOrder[1:]
		delete(rm.completedRequests, evictID)
	}
}

func (rm *RunManager) shouldSampleCompletedRequest(requestID string) bool {
	if rm.traceSamplingRate >= 1.0 {
		return true
	}
	if rm.traceSamplingRate <= 0 {
		return false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(requestID))
	v := float64(h.Sum32()) / float64(^uint32(0))
	return v < rm.traceSamplingRate
}

// RecordLatency records a request latency
func (rm *RunManager) RecordLatency(latencyMs float64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.latencySummary.record(latencyMs)
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
	totalRequests := rm.totalRequests
	failedRequests := rm.failedCount
	successfulRequests := totalRequests - failedRequests

	latency := rm.latencySummary.snapshot()
	latencyP50, latencyP95, latencyP99, latencyMean := latency.P50, latency.P95, latency.P99, latency.Mean

	duration := rm.run.EndTime.Sub(rm.run.StartTime)
	throughputRPS := 0.0
	if duration > 0 {
		throughputRPS = float64(totalRequests) / duration.Seconds()
	}

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
	rm.mu.Lock()
	defer rm.mu.Unlock()

	totalRequests := int(rm.totalRequests)
	totalTraces := len(rm.traces)
	completedRequests := int(rm.completedCount)
	failedRequests := int(rm.failedCount)
	for _, req := range rm.requests {
		if req.Status == models.RequestStatusCompleted {
			completedRequests++
		} else if req.Status == models.RequestStatusFailed || req.Error != "" {
			failedRequests++
		}
	}

	latency := rm.latencySummary.snapshot()
	currentLatencyP50, currentLatencyP95 := latency.P50, latency.P95

	elapsed := time.Since(rm.run.StartTime)

	throughput := 0.0
	if elapsed > 0 {
		throughput = float64(totalRequests) / elapsed.Seconds()
	}

	return map[string]interface{}{
		"status":             rm.run.Status,
		"elapsed":            elapsed.String(),
		"total_requests":     totalRequests,
		"active_requests":    len(rm.requests),
		"completed_requests": completedRequests,
		"failed_requests":    failedRequests,
		"total_traces":       totalTraces,
		"current_p50_ms":     fmt.Sprintf("%.2f", currentLatencyP50),
		"current_p95_ms":     fmt.Sprintf("%.2f", currentLatencyP95),
		"throughput_rps":     fmt.Sprintf("%.2f", throughput),
	}
}

func (rm *RunManager) Snapshot() RunManagerSnapshot {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return RunManagerSnapshot{
		ActiveRequests:           len(rm.requests),
		TotalRequests:            rm.totalRequests,
		CompletedRequests:        rm.completedCount,
		FailedRequests:           rm.failedCount,
		RetainedCompletedSamples: len(rm.completedRequests),
	}
}
