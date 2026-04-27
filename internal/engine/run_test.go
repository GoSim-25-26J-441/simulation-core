package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestNewRunManager(t *testing.T) {
	rm := NewRunManager("run-123")
	if rm == nil {
		t.Fatal("NewRunManager returned nil")
	}

	run := rm.GetRun()
	if run.ID != "run-123" {
		t.Errorf("Expected run ID 'run-123', got '%s'", run.ID)
	}
	if run.Status != models.RunStatusPending {
		t.Errorf("Expected status pending, got %s", run.Status)
	}
}

func TestRunManagerLifecycle(t *testing.T) {
	rm := NewRunManager("run-test")

	// Initial status
	run := rm.GetRun()
	if run.Status != models.RunStatusPending {
		t.Errorf("Expected initial status pending, got %s", run.Status)
	}

	// Start
	rm.Start()
	run = rm.GetRun()
	if run.Status != models.RunStatusRunning {
		t.Errorf("Expected status running after Start(), got %s", run.Status)
	}

	// Sleep briefly to ensure duration is measurable
	time.Sleep(10 * time.Millisecond)

	// Complete
	rm.Complete()
	run = rm.GetRun()
	if run.Status != models.RunStatusCompleted {
		t.Errorf("Expected status completed after Complete(), got %s", run.Status)
	}
	if run.Duration == 0 {
		t.Error("Expected non-zero duration after Complete()")
	}
	if run.Metrics == nil {
		t.Error("Expected metrics to be calculated after Complete()")
	}
}

func TestRunManagerFail(t *testing.T) {
	rm := NewRunManager("run-fail")
	rm.Start()

	err := &testError{msg: "test error"}
	rm.Fail(err)

	run := rm.GetRun()
	if run.Status != models.RunStatusFailed {
		t.Errorf("Expected status failed, got %s", run.Status)
	}
	if run.Error != "test error" {
		t.Errorf("Expected error 'test error', got '%s'", run.Error)
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

func TestRunManagerTraces(t *testing.T) {
	rm := NewRunManager("run-traces")

	trace1 := &models.Trace{
		ID:            "trace-1",
		RootRequestID: "req-1",
		StartTime:     time.Now(),
	}
	trace2 := &models.Trace{
		ID:            "trace-2",
		RootRequestID: "req-2",
		StartTime:     time.Now(),
	}

	rm.AddTrace(trace1)
	rm.AddTrace(trace2)

	// Retrieve traces
	retrieved, ok := rm.GetTrace("trace-1")
	if !ok {
		t.Error("Expected to find trace-1")
	}
	if retrieved.ID != "trace-1" {
		t.Errorf("Expected trace ID 'trace-1', got '%s'", retrieved.ID)
	}

	_, ok = rm.GetTrace("trace-2")
	if !ok {
		t.Error("Expected to find trace-2")
	}

	_, ok = rm.GetTrace("trace-999")
	if ok {
		t.Error("Should not find non-existent trace")
	}
}

func TestRunManagerRequests(t *testing.T) {
	rm := NewRunManager("run-requests")

	req1 := &models.Request{
		ID:          "req-1",
		TraceID:     "trace-1",
		ServiceName: "service-a",
		Status:      models.RequestStatusCompleted,
	}
	req2 := &models.Request{
		ID:          "req-2",
		TraceID:     "trace-2",
		ServiceName: "service-b",
		Status:      models.RequestStatusFailed,
		Error:       "timeout",
	}

	rm.AddRequest(req1)
	rm.AddRequest(req2)

	// Retrieve requests
	retrieved, ok := rm.GetRequest("req-1")
	if !ok {
		t.Error("Expected to find req-1")
	}
	if retrieved.ServiceName != "service-a" {
		t.Errorf("Expected service 'service-a', got '%s'", retrieved.ServiceName)
	}

	retrieved, ok = rm.GetRequest("req-2")
	if !ok {
		t.Error("Expected to find req-2")
	}
	if retrieved.Error != "timeout" {
		t.Errorf("Expected error 'timeout', got '%s'", retrieved.Error)
	}

	_, ok = rm.GetRequest("req-999")
	if ok {
		t.Error("Should not find non-existent request")
	}
}

func TestRunManagerListRequestsSnapshot(t *testing.T) {
	rm := NewRunManager("run-list-requests")
	rm.AddRequest(&models.Request{ID: "req-a", ServiceName: "svc-a"})
	rm.AddRequest(&models.Request{ID: "req-b", ServiceName: "svc-b"})

	list := rm.ListRequests()
	if len(list) != 2 {
		t.Fatalf("expected 2 requests in snapshot, got %d", len(list))
	}
	ids := map[string]bool{list[0].ID: true, list[1].ID: true}
	if !ids["req-a"] || !ids["req-b"] {
		t.Fatalf("expected request IDs req-a and req-b, got %#v", ids)
	}
}

func TestRunManagerLatencies(t *testing.T) {
	rm := NewRunManager("run-latencies")

	latencies := []float64{10.0, 20.0, 30.0, 40.0, 50.0}
	for _, lat := range latencies {
		rm.RecordLatency(lat)
	}

	rm.Start()
	time.Sleep(10 * time.Millisecond)
	rm.Complete()

	metrics := rm.GetRun().Metrics
	if metrics == nil {
		t.Fatal("Expected metrics to be calculated")
	}

	// P50 should be 30.0
	if metrics.LatencyP50 != 30.0 {
		t.Errorf("Expected P50 latency 30.0, got %f", metrics.LatencyP50)
	}

	// Mean should be 30.0
	if metrics.LatencyMean != 30.0 {
		t.Errorf("Expected mean latency 30.0, got %f", metrics.LatencyMean)
	}
}

func TestRunManagerLatencyReservoirIsBounded(t *testing.T) {
	t.Setenv("SIMD_RUN_LATENCY_RESERVOIR_SIZE", "16")
	rm := NewRunManager("run-latency-bound")
	for i := 0; i < 5000; i++ {
		rm.RecordLatency(float64(i))
	}
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	if got := len(rm.latencySummary.reservoir); got != 16 {
		t.Fatalf("expected bounded latency reservoir size 16, got %d", got)
	}
	if rm.latencySummary.count != 5000 {
		t.Fatalf("expected latency count 5000, got %d", rm.latencySummary.count)
	}
}

func TestRunManagerLatencyPercentilesFromSummary(t *testing.T) {
	t.Setenv("SIMD_RUN_LATENCY_RESERVOIR_SIZE", "4096")
	rm := NewRunManager("run-latency-percentiles")
	rm.Start()
	for i := 1; i <= 100; i++ {
		rm.RecordLatency(float64(i))
	}
	time.Sleep(2 * time.Millisecond)
	rm.Complete()

	metrics := rm.GetRun().Metrics
	if metrics == nil {
		t.Fatal("expected metrics")
	}
	if metrics.LatencyP50 <= 0 || metrics.LatencyP95 <= 0 || metrics.LatencyP99 <= 0 {
		t.Fatalf("expected populated latency percentiles, got p50=%f p95=%f p99=%f", metrics.LatencyP50, metrics.LatencyP95, metrics.LatencyP99)
	}
	if metrics.LatencyP95 < metrics.LatencyP50 || metrics.LatencyP99 < metrics.LatencyP95 {
		t.Fatalf("expected ordered percentiles, got p50=%f p95=%f p99=%f", metrics.LatencyP50, metrics.LatencyP95, metrics.LatencyP99)
	}
	if metrics.LatencyMean <= 0 {
		t.Fatalf("expected non-zero mean latency, got %f", metrics.LatencyMean)
	}
}

func TestRunManagerConcurrentRecordLatencyAndGetStats(t *testing.T) {
	t.Setenv("SIMD_RUN_LATENCY_RESERVOIR_SIZE", "256")
	rm := NewRunManager("run-latency-concurrent")
	rm.Start()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				rm.RecordLatency(float64(offset*1000 + j))
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 400; j++ {
				stats := rm.GetStats()
				if _, ok := stats["current_p50_ms"]; !ok {
					t.Errorf("expected current_p50_ms in stats")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestRunManagerServiceMetrics(t *testing.T) {
	rm := NewRunManager("run-svc-metrics")

	metrics := &models.ServiceMetrics{
		ServiceName:  "test-service",
		RequestCount: 100,
		ErrorCount:   5,
		LatencyP50:   25.5,
		LatencyP95:   95.2,
	}

	rm.UpdateServiceMetrics("test-service", metrics)

	retrieved, ok := rm.GetServiceMetrics("test-service")
	if !ok {
		t.Error("Expected to find service metrics")
	}
	if retrieved.ServiceName != "test-service" {
		t.Errorf("Expected service name 'test-service', got '%s'", retrieved.ServiceName)
	}
	if retrieved.RequestCount != 100 {
		t.Errorf("Expected request count 100, got %d", retrieved.RequestCount)
	}

	_, ok = rm.GetServiceMetrics("nonexistent")
	if ok {
		t.Error("Should not find non-existent service metrics")
	}
}

func TestRunManagerConfig(t *testing.T) {
	rm := NewRunManager("run-config")

	rm.SetConfig("key1", "value1")
	rm.SetConfig("key2", 42)
	rm.SetConfig("key3", true)

	val, ok := rm.GetConfig("key1")
	if !ok {
		t.Error("Expected to find key1")
	}
	if val != "value1" {
		t.Errorf("Expected value 'value1', got '%v'", val)
	}

	val, ok = rm.GetConfig("key2")
	if !ok {
		t.Error("Expected to find key2")
	}
	if val != 42 {
		t.Errorf("Expected value 42, got '%v'", val)
	}

	_, ok = rm.GetConfig("nonexistent")
	if ok {
		t.Error("Should not find non-existent config key")
	}
}

func TestRunManagerMetadata(t *testing.T) {
	rm := NewRunManager("run-metadata")

	rm.SetMetadata("author", "test")
	rm.SetMetadata("version", "1.0.0")

	val, ok := rm.GetMetadata("author")
	if !ok {
		t.Error("Expected to find author")
	}
	if val != "test" {
		t.Errorf("Expected value 'test', got '%s'", val)
	}

	val, ok = rm.GetMetadata("version")
	if !ok {
		t.Error("Expected to find version")
	}
	if val != "1.0.0" {
		t.Errorf("Expected value '1.0.0', got '%s'", val)
	}

	_, ok = rm.GetMetadata("nonexistent")
	if ok {
		t.Error("Should not find non-existent metadata key")
	}
}

func TestRunManagerStats(t *testing.T) {
	rm := NewRunManager("run-stats")
	rm.Start()

	// Add some requests
	req1 := &models.Request{ID: "req-1", Status: models.RequestStatusCompleted}
	req2 := &models.Request{ID: "req-2", Status: models.RequestStatusCompleted}
	req3 := &models.Request{ID: "req-3", Status: models.RequestStatusFailed}

	rm.AddRequest(req1)
	rm.AddRequest(req2)
	rm.AddRequest(req3)

	// Add some latencies
	rm.RecordLatency(10.0)
	rm.RecordLatency(20.0)
	rm.RecordLatency(30.0)

	stats := rm.GetStats()
	if stats["total_requests"] != 3 {
		t.Errorf("Expected 3 total requests, got %v", stats["total_requests"])
	}
	if stats["completed_requests"] != 2 {
		t.Errorf("Expected 2 completed requests, got %v", stats["completed_requests"])
	}
	if stats["failed_requests"] != 1 {
		t.Errorf("Expected 1 failed request, got %v", stats["failed_requests"])
	}
	if stats["status"] != models.RunStatusRunning {
		t.Errorf("Expected status running, got %v", stats["status"])
	}
}

func TestRunManagerContext(t *testing.T) {
	rm := NewRunManager("run-context")

	ctx := rm.Context()
	if ctx == nil {
		t.Fatal("Expected non-nil context")
	}

	// Context should not be cancelled initially
	select {
	case <-ctx.Done():
		t.Error("Context should not be cancelled initially")
	default:
	}

	// Cancel the run
	rm.Cancel()

	// Context should now be cancelled
	select {
	case <-ctx.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Context should be cancelled after Cancel()")
	}
}

func TestRunManagerMaxRequestsTrackedLimit(t *testing.T) {
	rm := NewRunManager("run-limit")
	limitReached := false
	rm.SetMaxRequestsTracked(1, func(currentCount, max int) {
		limitReached = true
		if currentCount <= max {
			t.Fatalf("expected currentCount > max, got %d <= %d", currentCount, max)
		}
	})
	rm.AddRequest(&models.Request{ID: "req-1"})
	rm.AddRequest(&models.Request{ID: "req-2"})
	if !limitReached {
		t.Fatalf("expected limit callback")
	}
	if got := len(rm.ListRequests()); got != 1 {
		t.Fatalf("expected only first request retained, got %d", got)
	}
}

func TestRunManagerMaxTotalRequestsLimit(t *testing.T) {
	rm := NewRunManager("run-total-limit")
	limitReached := false
	rm.SetMaxTotalRequests(1, func(currentCount, max int) {
		limitReached = true
		if currentCount <= max {
			t.Fatalf("expected currentCount > max, got %d <= %d", currentCount, max)
		}
	})
	req := &models.Request{ID: "req-1", Status: models.RequestStatusCompleted}
	rm.AddRequest(req)
	rm.FinalizeRequest(req)
	rm.AddRequest(&models.Request{ID: "req-2"})
	if !limitReached {
		t.Fatalf("expected total-request limit callback")
	}
	if got := rm.GetStats()["total_requests"]; got != 1 {
		t.Fatalf("expected total_requests to remain capped at 1, got %v", got)
	}
}

func TestRunManagerSeparateActiveAndTotalLimitCallbacks(t *testing.T) {
	rm := NewRunManager("run-dual-limits")
	activeHits := 0
	totalHits := 0
	rm.SetMaxRequestsTracked(1, func(currentCount, max int) {
		activeHits++
		if max != 1 {
			t.Fatalf("expected active max=1, got %d", max)
		}
	})
	rm.SetMaxTotalRequests(100, func(currentCount, max int) {
		totalHits++
	})
	rm.AddRequest(&models.Request{ID: "req-1"})
	rm.AddRequest(&models.Request{ID: "req-2"})
	if activeHits != 1 {
		t.Fatalf("expected active limit callback once, got %d", activeHits)
	}
	if totalHits != 0 {
		t.Fatalf("expected total limit callback not to fire, got %d", totalHits)
	}
}

func TestRunManagerFinalizePrunesActiveRequest(t *testing.T) {
	t.Setenv("SIMD_MAX_COMPLETED_REQUEST_TRACES", "2")
	rm := NewRunManager("run-prune")
	req := &models.Request{
		ID:     "req-1",
		Status: models.RequestStatusCompleted,
	}
	rm.AddRequest(req)
	rm.FinalizeRequest(req)

	if got := len(rm.requests); got != 0 {
		t.Fatalf("expected active requests to be pruned, got %d", got)
	}
	r, ok := rm.GetRequest("req-1")
	if !ok || r == nil {
		t.Fatalf("expected finalized request to remain in completed sample")
	}
}

func TestRunManagerCompletedSamplesAreBounded(t *testing.T) {
	t.Setenv("SIMD_MAX_COMPLETED_REQUEST_TRACES", "2")
	rm := NewRunManager("run-prune-cap")

	for i := 1; i <= 3; i++ {
		id := "req-" + string(rune('0'+i))
		req := &models.Request{ID: id, Status: models.RequestStatusCompleted}
		rm.AddRequest(req)
		rm.FinalizeRequest(req)
	}

	if _, ok := rm.GetRequest("req-1"); ok {
		t.Fatalf("expected oldest completed request to be evicted")
	}
	if _, ok := rm.GetRequest("req-2"); !ok {
		t.Fatalf("expected req-2 to remain")
	}
	if _, ok := rm.GetRequest("req-3"); !ok {
		t.Fatalf("expected req-3 to remain")
	}
}
