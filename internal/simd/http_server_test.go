package simd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
)

const testScenarioYAML = `
hosts:
  - id: host-1
    cores: 2
services:
  - id: svc1
    replicas: 1
    model: cpu
    endpoints:
      - path: /test
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: svc1:/test
    arrival: {type: poisson, rate_rps: 10}
`

func TestHTTPServerHealthz(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body["status"])
	}
	if body["timestamp"] == "" {
		t.Fatalf("expected timestamp to be set")
	}
}

func TestHTTPServerCreateRun(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	reqBody := map[string]any{
		"input": map[string]any{
			"scenario_yaml": testScenarioYAML,
			"duration_ms":   100,
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	run, ok := resp["run"].(map[string]any)
	if !ok {
		t.Fatalf("expected run in response")
	}
	if run["id"] == "" {
		t.Fatalf("expected run id to be set")
	}
}

func TestHTTPServerGetRun(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id, nil)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	run, ok := resp["run"].(map[string]any)
	if !ok {
		t.Fatalf("expected run in response")
	}
	if run["id"] != rec.Run.Id {
		t.Fatalf("expected run id %s, got %v", rec.Run.Id, run["id"])
	}
}

func TestHTTPServerGetRunNotFound(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/nonexistent", nil)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestHTTPServerStartRun(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+rec.Run.Id, nil)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	run, ok := resp["run"].(map[string]any)
	if !ok {
		t.Fatalf("expected run in response")
	}
	if run["id"] != rec.Run.Id {
		t.Fatalf("expected run id %s, got %v", rec.Run.Id, run["id"])
	}
	// Run may still be RUNNING or already COMPLETED (short duration)
	status, _ := run["status"].(string)
	if status != "RUN_STATUS_RUNNING" && status != "RUN_STATUS_COMPLETED" {
		t.Fatalf("expected running or completed status, got %v", run["status"])
	}
}

func TestHTTPServerStartRunNotFound(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/nonexistent", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPServerStopRun(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   500, // Short duration for test
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Start the run
	_, err = executor.Start(rec.Run.Id)
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+rec.Run.Id+":stop", nil)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	run, ok := resp["run"].(map[string]any)
	if !ok {
		t.Fatalf("expected run in response")
	}
	if run["status"] != "RUN_STATUS_STOPPED" {
		t.Fatalf("expected stopped status, got %v", run["status"])
	}
}

func TestHTTPServerGetRunMetrics(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Set metrics
	err = store.SetMetrics(rec.Run.Id, &simulationv1.RunMetrics{
		TotalRequests:      100,
		SuccessfulRequests: 95,
		FailedRequests:     5,
		LatencyP50Ms:       50.0,
		LatencyP95Ms:       100.0,
		LatencyP99Ms:       200.0,
		LatencyMeanMs:      75.0,
		ThroughputRps:      10.5,
	})
	if err != nil {
		t.Fatalf("SetMetrics error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/metrics", nil)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	metrics, ok := resp["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("expected metrics in response")
	}
	if metrics["total_requests"].(float64) != 100 {
		t.Fatalf("expected total_requests 100, got %v", metrics["total_requests"])
	}
}

func TestHTTPServerGetRunMetricsNotAvailable(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/metrics", nil)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected status 412, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPServerCreateRunWithInvalidID(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	tests := []struct {
		name  string
		runID string
	}{
		{"with colon", "test:stop"},
		{"with slash", "test/metrics"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqBody := map[string]any{
				"run_id": tt.runID,
				"input": map[string]any{
					"scenario_yaml": testScenarioYAML,
					"duration_ms":   100,
				},
			}
			bodyBytes, _ := json.Marshal(reqBody)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(string(bodyBytes)))
			req.Header.Set("Content-Type", "application/json")

			srv.Handler().ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400 for run ID %q, got %d: %s", tt.runID, rr.Code, rr.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid json: %v", err)
			}
			errMsg, ok := resp["error"].(string)
			if !ok || !strings.Contains(errMsg, "cannot contain") {
				t.Fatalf("expected validation error message, got: %v", resp["error"])
			}
		})
	}
}

func TestHTTPServerTimeSeries(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create a run
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	}
	rec, err := store.Create("test-run", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Create and store a collector with some test data
	collector := metrics.NewCollector()
	collector.Start()

	// Record some test metrics
	now := time.Now()
	labels1 := map[string]string{"service": "svc1", "instance": "svc1-1"}
	labels2 := map[string]string{"service": "svc1", "instance": "svc1-2"}

	collector.Record("cpu_utilization", 0.65, now, labels1)
	collector.Record("cpu_utilization", 0.72, now.Add(time.Second), labels1)
	collector.Record("cpu_utilization", 0.58, now, labels2)
	collector.Record("memory_utilization", 0.45, now, labels1)

	collector.Stop()

	// Store collector
	if err := store.SetCollector(rec.Run.Id, collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	// Test: Get all time-series data
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/timeseries", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if body["run_id"] != "test-run" {
		t.Fatalf("expected run_id test-run, got %v", body["run_id"])
	}

	points, ok := body["points"].([]any)
	if !ok {
		t.Fatalf("expected points array, got %T", body["points"])
	}

	if len(points) != 4 {
		t.Fatalf("expected 4 points, got %d", len(points))
	}
}

func TestHTTPServerTimeSeriesWithFilters(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create a run
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	}
	rec, err := store.Create("test-run", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Create and store a collector with test data
	collector := metrics.NewCollector()
	collector.Start()

	now := time.Now()
	labels1 := map[string]string{"service": "svc1", "instance": "svc1-1"}
	labels2 := map[string]string{"service": "svc2", "instance": "svc2-1"}

	collector.Record("cpu_utilization", 0.65, now, labels1)
	collector.Record("cpu_utilization", 0.72, now.Add(time.Second), labels1)
	collector.Record("cpu_utilization", 0.58, now, labels2)
	collector.Record("memory_utilization", 0.45, now, labels1)

	collector.Stop()

	if err := store.SetCollector(rec.Run.Id, collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	// Test: Filter by metric name
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/timeseries?metric=cpu_utilization", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	points, ok := body["points"].([]any)
	if !ok {
		t.Fatalf("expected points array")
	}

	if len(points) != 3 {
		t.Fatalf("expected 3 cpu_utilization points, got %d", len(points))
	}

	// Test: Filter by service
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/timeseries?service=svc1", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	points, ok = body["points"].([]any)
	if !ok {
		t.Fatalf("expected points array")
	}

	if len(points) != 3 {
		t.Fatalf("expected 3 points for svc1, got %d", len(points))
	}

	// Test: Filter by metric and service
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/timeseries?metric=cpu_utilization&service=svc1", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	points, ok = body["points"].([]any)
	if !ok {
		t.Fatalf("expected points array")
	}

	if len(points) != 2 {
		t.Fatalf("expected 2 cpu_utilization points for svc1, got %d", len(points))
	}
}

// TestHTTPServerTimeSeriesRequestCountCumulative verifies that the timeseries endpoint
// returns cumulative values for request_count (and not raw increments), so the backend
// can persist without client-side summing.
func TestHTTPServerTimeSeriesRequestCountCumulative(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	}
	rec, err := store.Create("test-run", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	collector := metrics.NewCollector()
	collector.Start()

	now := time.Now()
	labels := map[string]string{"service": "svc1", "endpoint": "/test"}
	// Record 5 request_count points (value 1 each, as the simulator does)
	for i := 0; i < 5; i++ {
		metrics.RecordRequestCount(collector, 1.0, now.Add(time.Duration(i)*time.Millisecond), labels)
	}
	collector.Stop()

	if err := store.SetCollector(rec.Run.Id, collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/timeseries?metric=request_count", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	points, ok := body["points"].([]any)
	if !ok {
		t.Fatalf("expected points array, got %T", body["points"])
	}
	if len(points) != 5 {
		t.Fatalf("expected 5 points, got %d", len(points))
	}

	// Values must be cumulative: 1, 2, 3, 4, 5 (non-decreasing; last = 5)
	var prevVal float64
	for i, p := range points {
		pm, ok := p.(map[string]any)
		if !ok {
			t.Fatalf("point %d: expected map, got %T", i, p)
		}
		v, _ := pm["value"].(float64)
		if v <= prevVal && i > 0 {
			t.Fatalf("point %d: expected cumulative (value > %f), got value %f", i, prevVal, v)
		}
		prevVal = v
	}
	if prevVal != 5 {
		t.Fatalf("expected last cumulative value 5, got %f", prevVal)
	}
}

func TestHTTPServerTimeSeriesNotFound(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/nonexistent/metrics/timeseries", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestHTTPServerTimeSeriesNoCollector(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	// Create a run without collector
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	}
	_, err := store.Create("test-run", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/timeseries", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected status 412, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPServerTimeSeriesWithTimeRange(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create a run
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	}
	rec, err := store.Create("test-run", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Create collector with time-stamped data
	collector := metrics.NewCollector()
	collector.Start()

	baseTime := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	labels := map[string]string{"service": "svc1"}

	collector.Record("cpu_utilization", 0.65, baseTime, labels)
	collector.Record("cpu_utilization", 0.72, baseTime.Add(2*time.Second), labels)
	collector.Record("cpu_utilization", 0.58, baseTime.Add(5*time.Second), labels)

	collector.Stop()

	if err := store.SetCollector(rec.Run.Id, collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	// Test: Filter by time range (Unix milliseconds)
	startTime := baseTime.Add(1 * time.Second).UnixMilli()
	endTime := baseTime.Add(4 * time.Second).UnixMilli()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/timeseries?start_time="+strconv.FormatInt(startTime, 10)+"&end_time="+strconv.FormatInt(endTime, 10), nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	points, ok := body["points"].([]any)
	if !ok {
		t.Fatalf("expected points array")
	}

	// Should have 1 point in the range (the one at baseTime + 2s)
	if len(points) != 1 {
		t.Fatalf("expected 1 point in time range, got %d", len(points))
	}
}

func TestHTTPServerMetricsStream(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create a run
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	}
	rec, err := store.Create("test-run", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Create and store a collector with test data
	collector := metrics.NewCollector()
	collector.Start()

	now := time.Now()
	labels := map[string]string{"service": "svc1", "instance": "svc1-1"}
	collector.Record("cpu_utilization", 0.65, now, labels)

	collector.Stop()

	if err := store.SetCollector(rec.Run.Id, collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	// Test SSE endpoint with timeout
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/stream", nil)
	req.Header.Set("Accept", "text/event-stream")

	// Create context with timeout
	ctx, cancel := context.WithTimeout(req.Context(), 200*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	// Start streaming (will timeout after 200ms)
	srv.Handler().ServeHTTP(rr, req)

	// Check response headers
	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream, got %s", rr.Header().Get("Content-Type"))
	}

	if rr.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("expected Cache-Control no-cache, got %s", rr.Header().Get("Cache-Control"))
	}

	// Check that we received SSE events
	body := rr.Body.String()
	if !strings.Contains(body, "event:") {
		t.Fatalf("expected SSE event format, got: %s", body)
	}
}

func TestHTTPServerMetricsStreamNotFound(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/nonexistent/metrics/stream", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestHostMetricsFromCollector(t *testing.T) {
	collector := metrics.NewCollector()
	collector.Start()
	now := time.Now()

	// No host-labelled metrics: should return nil
	got := hostMetricsFromCollector(collector, nil)
	if got != nil {
		t.Fatalf("expected nil when no host labels, got %d entries", len(got))
	}

	// Record host-level metrics for two hosts
	metrics.RecordCPUUtilization(collector, 0.1, now, metrics.CreateHostLabels("host-1"))
	metrics.RecordMemoryUtilization(collector, 0.2, now, metrics.CreateHostLabels("host-1"))
	metrics.RecordCPUUtilization(collector, 0.3, now.Add(time.Second), metrics.CreateHostLabels("host-2"))
	metrics.RecordMemoryUtilization(collector, 0.4, now, metrics.CreateHostLabels("host-2"))

	got = hostMetricsFromCollector(collector, nil)
	if len(got) != 2 {
		t.Fatalf("expected 2 host entries, got %d", len(got))
	}
	// Sorted by host_id: host-1, host-2
	if got[0]["host_id"] != "host-1" || got[1]["host_id"] != "host-2" {
		t.Fatalf("expected host_id order host-1, host-2, got %v, %v", got[0]["host_id"], got[1]["host_id"])
	}
	if got[0]["cpu_utilization"].(float64) != 0.1 || got[0]["memory_utilization"].(float64) != 0.2 {
		t.Fatalf("host-1: expected cpu=0.1 mem=0.2, got cpu=%v mem=%v", got[0]["cpu_utilization"], got[0]["memory_utilization"])
	}
	if got[1]["cpu_utilization"].(float64) != 0.3 || got[1]["memory_utilization"].(float64) != 0.4 {
		t.Fatalf("host-2: expected cpu=0.3 mem=0.4, got cpu=%v mem=%v", got[1]["cpu_utilization"], got[1]["memory_utilization"])
	}

	// Inventory mode: include hosts with no samples yet (zeros), aligned with RM host list.
	gotInv := hostMetricsFromCollector(collector, []string{"host-0", "host-1", "host-2"})
	if len(gotInv) != 3 {
		t.Fatalf("expected 3 inventory entries, got %d", len(gotInv))
	}
	if gotInv[0]["host_id"] != "host-0" {
		t.Fatalf("expected first host host-0, got %v", gotInv[0]["host_id"])
	}
	if gotInv[0]["cpu_utilization"].(float64) != 0 || gotInv[0]["memory_utilization"].(float64) != 0 {
		t.Fatalf("host-0 idle: expected cpu=0 mem=0, got cpu=%v mem=%v", gotInv[0]["cpu_utilization"], gotInv[0]["memory_utilization"])
	}
	if gotInv[1]["host_id"] != "host-1" || gotInv[2]["host_id"] != "host-2" {
		t.Fatalf("unexpected inventory order: %v, %v", gotInv[1]["host_id"], gotInv[2]["host_id"])
	}
}

// sseEventTypesFromBody returns SSE event names in order of appearance (each "event: …" line).
func sseEventTypesFromBody(body string) []string {
	var seq []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "event: ") {
			seq = append(seq, strings.TrimSpace(strings.TrimPrefix(line, "event: ")))
		}
	}
	return seq
}

// TestHTTPServerMetricsStreamNonRealtimeCompletionOrder asserts that when a non-realtime run
// finishes, the stream ends with status_change(COMPLETED) → metrics_snapshot → complete so
// clients receive final metrics before the terminal complete event.
func TestHTTPServerMetricsStreamNonRealtimeCompletionOrder(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	const runID = "nr-sse-completion-order"
	_, err := store.Create(runID, &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   5000,
		RealTimeMode: false,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus RUNNING: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		if err := store.SetMetrics(runID, &simulationv1.RunMetrics{
			TotalRequests:      7,
			SuccessfulRequests: 7,
			LatencyP50Ms:       2.0,
			LatencyMeanMs:      2.5,
		}); err != nil {
			errCh <- err
			return
		}
		if _, err := store.SetStatus(runID, simulationv1.RunStatus_RUN_STATUS_COMPLETED, ""); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+runID+"/metrics/stream?interval_ms=25", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	srv.Handler().ServeHTTP(rr, req)

	if err := <-errCh; err != nil {
		t.Fatalf("background store update: %v", err)
	}

	seq := sseEventTypesFromBody(rr.Body.String())
	if len(seq) < 3 {
		t.Fatalf("expected at least 3 SSE events, got %d: %v", len(seq), seq)
	}
	n := len(seq)
	if seq[n-3] != "status_change" || seq[n-2] != "metrics_snapshot" || seq[n-1] != "complete" {
		start := n - 6
		if start < 0 {
			start = 0
		}
		t.Fatalf("expected final triple status_change, metrics_snapshot, complete; got tail %v", seq[start:])
	}
	if !strings.Contains(rr.Body.String(), "RUN_STATUS_COMPLETED") {
		t.Fatal("expected completed status in stream body")
	}
}

func TestHTTPServerMetricsStreamSnapshotIncludesHostMetrics(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	rec, err := store.Create("test-run-hm", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
		RealTimeMode: true, // live collector enrichment (host_metrics) in SSE
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if err := store.SetMetrics(rec.Run.Id, &simulationv1.RunMetrics{
		TotalRequests:      10,
		SuccessfulRequests: 10,
		LatencyP50Ms:       5.0,
		LatencyMeanMs:      6.0,
	}); err != nil {
		t.Fatalf("SetMetrics error: %v", err)
	}
	if _, err := store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}

	collector := metrics.NewCollector()
	collector.Start()
	now := time.Now()
	metrics.RecordCPUUtilization(collector, 0.15, now, metrics.CreateHostLabels("host-1"))
	metrics.RecordMemoryUtilization(collector, 0.25, now, metrics.CreateHostLabels("host-1"))
	collector.Stop()
	if err := store.SetCollector(rec.Run.Id, collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run-hm/metrics/stream?interval_ms=50", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 300*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	srv.Handler().ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "event: metrics_snapshot") {
		t.Fatalf("expected metrics_snapshot event in stream, got: %s", body[:min(200, len(body))])
	}
	if !strings.Contains(body, "host_metrics") {
		t.Error("expected host_metrics in metrics_snapshot payload")
	}
	if !strings.Contains(body, "\"host_id\"") {
		t.Error("expected host_id in SSE stream")
	}
	if !strings.Contains(body, "host-1") {
		t.Error("expected host-1 in SSE stream")
	}
}

func TestHTTPServerMetricsStreamSnapshotIncludesPlacementsInResources(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	rec, err := store.Create("test-run-placements-sse", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   120000,
		RealTimeMode: true,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := executor.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer func() { _, _ = executor.Stop(rec.Run.Id) }()

	time.Sleep(150 * time.Millisecond)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/metrics/stream?interval_ms=25", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)
	srv.Handler().ServeHTTP(rr, req)

	body := rr.Body.String()
	if !strings.Contains(body, "event: metrics_snapshot") {
		t.Fatalf("expected metrics_snapshot event in stream, got: %s", body[:min(200, len(body))])
	}
	if !strings.Contains(body, `"placements"`) {
		t.Fatal("expected resources.placements (placements key) in metrics_snapshot payload")
	}
	if !strings.Contains(body, `"resources"`) {
		t.Fatal("expected resources object in metrics_snapshot payload")
	}
}

func TestHTTPServerMetricsStreamWithInterval(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create a run
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	}
	rec, err := store.Create("test-run", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Create and store a collector
	collector := metrics.NewCollector()
	collector.Start()
	collector.Stop()

	if err := store.SetCollector(rec.Run.Id, collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	// Test with custom interval
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/stream?interval_ms=500", nil)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(req.Context(), 200*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	// Start streaming (will timeout)
	srv.Handler().ServeHTTP(rr, req)

	// Check headers
	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream")
	}
}

func TestHTTPServerMetricsStreamOptimizationProgress(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	// Create an optimization run
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
		Optimization: &simulationv1.OptimizationConfig{
			Objective:     "p95_latency_ms",
			MaxIterations: 5,
			StepSize:      1.0,
		},
	}
	rec, err := store.Create("opt-run", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Set status to running and simulate progress
	_, _ = store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, "")
	store.SetOptimizationProgress(rec.Run.Id, 1, 12.5)

	// Connect to metrics stream - use short interval and longer timeout for reliability
	// under -race (race detector slows execution significantly)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/opt-run/metrics/stream?interval_ms=10", nil)
	ctx, cancel := context.WithTimeout(req.Context(), 500*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream")
	}

	// Parse SSE output and look for optimization_progress event
	body := rr.Body.String()
	if !strings.Contains(body, "event: optimization_progress") {
		t.Fatalf("expected optimization_progress event in stream, got: %s", body)
	}
	if !strings.Contains(body, `"iteration":1`) {
		t.Fatalf("expected iteration 1 in optimization_progress")
	}
	// Check best_score flexibly (JSON may format 12.5 as "12.5" or "1.25e+01" on some platforms)
	if !strings.Contains(body, "12.5") && !strings.Contains(body, "1.25e+01") {
		t.Fatalf("expected best_score 12.5 in optimization_progress, got: %s", body)
	}
	// Default objective/unit when not set
	if !strings.Contains(body, `"objective":"p95_latency"`) && !strings.Contains(body, `"objective": "p95_latency"`) {
		t.Errorf("expected objective p95_latency in optimization_progress, got: %s", body)
	}
	if !strings.Contains(body, `"unit":"ms"`) && !strings.Contains(body, `"unit": "ms"`) {
		t.Errorf("expected unit ms in optimization_progress, got: %s", body)
	}
}

func TestHTTPServerMetricsStreamTimeSeriesData(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create a run (live SSE metrics require real-time mode)
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
		RealTimeMode: true,
	}
	rec, err := store.Create("test-run-ts", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Create and populate a collector with time-series metrics
	collector := metrics.NewCollector()
	collector.Start()

	// Record multiple time-series data points
	baseTime := time.Now()
	for i := 0; i < 5; i++ {
		timestamp := baseTime.Add(time.Duration(i) * 100 * time.Millisecond)
		collector.Record("cpu_usage", 50.0+float64(i)*5.0, timestamp, map[string]string{"service": "svc1"})
		collector.Record("request_rate", 10.0+float64(i)*2.0, timestamp, map[string]string{"endpoint": "/test"})
	}

	// Store the collector
	if err := store.SetCollector(rec.Run.Id, collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	// Set status to running
	if _, err := store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}

	// Test SSE streaming with fast interval
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run-ts/metrics/stream?interval_ms=100", nil)

	// Create context with timeout to collect events
	ctx, cancel := context.WithTimeout(req.Context(), 500*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	// Start streaming in goroutine to avoid blocking
	done := make(chan bool)
	go func() {
		srv.Handler().ServeHTTP(rr, req)
		done <- true
	}()

	// Wait for handler to complete before reading body (avoids data race on rr)
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("SSE stream did not complete within timeout")
	}

	// Check response headers
	if rr.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream, got %s", rr.Header().Get("Content-Type"))
	}

	// Get response body (safe to read after handler completed)
	body := rr.Body.String()
	t.Logf("SSE response body length: %d", len(body))

	// Verify we received SSE events
	if !strings.Contains(body, "event:") {
		t.Fatalf("expected SSE event format, got: %s", body)
	}

	// Verify status_change event was sent
	if !strings.Contains(body, "event: status_change") {
		t.Error("expected status_change event in SSE stream")
	}

	// Verify metric_update events were sent for time-series data
	if !strings.Contains(body, "event: metric_update") {
		t.Error("expected metric_update events in SSE stream for time-series data")
	}

	// Verify specific metric names in the stream
	if !strings.Contains(body, "cpu_usage") {
		t.Error("expected cpu_usage metric in SSE stream")
	}

	if !strings.Contains(body, "request_rate") {
		t.Error("expected request_rate metric in SSE stream")
	}

	// Verify metric data structure (should contain timestamp, metric, value, labels)
	if !strings.Contains(body, "\"metric\"") {
		t.Error("expected metric field in metric_update event data")
	}

	if !strings.Contains(body, "\"value\"") {
		t.Error("expected value field in metric_update event data")
	}

	if !strings.Contains(body, "\"timestamp\"") {
		t.Error("expected timestamp field in metric_update event data")
	}

	// Verify labels are included
	if !strings.Contains(body, "\"labels\"") {
		t.Error("expected labels field in metric_update event data")
	}

	// Parse and verify metric_update events structure
	lines := strings.Split(body, "\n")
	metricUpdateCount := 0
	metricNames := make(map[string]bool)

	for i, line := range lines {
		if strings.HasPrefix(line, "event: metric_update") {
			metricUpdateCount++
			// Next line should be data line
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "data:") {
				dataLine := strings.TrimPrefix(lines[i+1], "data: ")
				var data map[string]any
				if err := json.Unmarshal([]byte(dataLine), &data); err == nil {
					// Verify required fields
					if _, ok := data["timestamp"]; !ok {
						t.Error("metric_update event missing timestamp field")
					}
					if _, ok := data["metric"]; !ok {
						t.Error("metric_update event missing metric field")
					}
					if _, ok := data["value"]; !ok {
						t.Error("metric_update event missing value field")
					}
					if _, ok := data["labels"]; !ok {
						t.Error("metric_update event missing labels field")
					}
					// Verify metric name and track it
					if metricName, ok := data["metric"].(string); ok {
						metricNames[metricName] = true
						if metricName != "cpu_usage" && metricName != "request_rate" {
							t.Errorf("unexpected metric name: %s", metricName)
						}
					}
					// Verify timestamp format
					if tsStr, ok := data["timestamp"].(string); ok {
						if _, err := time.Parse(time.RFC3339Nano, tsStr); err != nil {
							t.Errorf("invalid timestamp format: %s, error: %v", tsStr, err)
						}
					}
					// Verify value is numeric
					if val, ok := data["value"].(float64); ok {
						if val < 0 {
							t.Errorf("unexpected negative value: %f", val)
						}
					}
				} else {
					t.Errorf("failed to parse metric_update data: %s, error: %v", dataLine, err)
				}
			}
		}
	}

	if metricUpdateCount == 0 {
		t.Error("no metric_update events found in SSE stream")
	} else {
		t.Logf("Received %d metric_update events", metricUpdateCount)
	}

	// Verify we received both metrics
	if len(metricNames) < 1 {
		t.Error("expected at least one unique metric name in time-series stream")
	}
}

func TestHTTPServerUpdateRunConfigurationVerticalScaling(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	// Create run
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
	}
	rec, err := store.Create("run-vert", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Wait briefly for initialization but ensure run is still RUNNING
	time.Sleep(10 * time.Millisecond)
	recLatest, ok := store.Get(rec.Run.Id)
	if !ok {
		t.Fatal("run not found after start")
	}
	if recLatest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run is not RUNNING (status=%v), skipping vertical scaling test", recLatest.Run.Status)
	}
	defer exec.Stop(rec.Run.Id)

	body := map[string]any{
		"services": []map[string]any{
			{
				"id":        "svc1",
				"replicas":  2,
				"cpu_cores": 4.0,
				"memory_mb": 2048.0,
			},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-vert/configuration", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 from PATCH configuration, got %d: %s", rr.Code, rr.Body.String())
	}

	cfg, ok := exec.GetRunConfiguration("run-vert")
	if !ok || cfg == nil {
		t.Fatalf("expected GetRunConfiguration to succeed after vertical scaling")
	}
	var svcCfg *simulationv1.ServiceConfigEntry
	for _, sCfg := range cfg.Services {
		if sCfg.ServiceId == "svc1" {
			svcCfg = sCfg
			break
		}
	}
	if svcCfg == nil {
		t.Fatalf("expected svc1 in run configuration")
	}
	if svcCfg.CpuCores != 4.0 {
		t.Fatalf("expected cpu_cores=4.0, got %f", svcCfg.CpuCores)
	}
	if svcCfg.MemoryMb != 2048.0 {
		t.Fatalf("expected memory_mb=2048.0, got %f", svcCfg.MemoryMb)
	}
}

func TestHTTPServerMetricsStreamMultipleTimePoints(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create a run (live SSE metrics require real-time mode)
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
		RealTimeMode: true,
	}
	rec, err := store.Create("test-run-multi", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Create a collector
	collector := metrics.NewCollector()
	collector.Start()

	// Store the collector first
	if err := store.SetCollector(rec.Run.Id, collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	// Set status to running
	if _, err := store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}

	// Test SSE streaming with fast interval
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run-multi/metrics/stream?interval_ms=150", nil)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(req.Context(), 800*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	// Start streaming in goroutine
	done := make(chan bool)
	go func() {
		srv.Handler().ServeHTTP(rr, req)
		done <- true
	}()

	// Add metrics progressively while streaming
	baseTime := time.Now()
	go func() {
		for i := 0; i < 3; i++ {
			timestamp := baseTime.Add(time.Duration(i) * 200 * time.Millisecond)
			collector.Record("latency_p50", 25.0+float64(i)*5.0, timestamp, map[string]string{"service": "svc1"})
			time.Sleep(200 * time.Millisecond)
		}
	}()

	// Wait for handler to complete before reading body (avoids data race on rr)
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("SSE stream did not complete within timeout")
	}

	// Get response body (safe to read after handler completed)
	body := rr.Body.String()

	// Verify we received multiple metric_update events
	metricUpdateCount := strings.Count(body, "event: metric_update")
	if metricUpdateCount < 2 {
		t.Errorf("expected at least 2 metric_update events, got %d", metricUpdateCount)
	}

	// Verify all events have proper structure
	lines := strings.Split(body, "\n")
	validEvents := 0
	for i, line := range lines {
		if strings.HasPrefix(line, "event: metric_update") && i+1 < len(lines) {
			if strings.HasPrefix(lines[i+1], "data:") {
				dataLine := strings.TrimPrefix(lines[i+1], "data: ")
				var data map[string]any
				if err := json.Unmarshal([]byte(dataLine), &data); err == nil {
					// Check all required fields
					if _, ok := data["timestamp"]; ok {
						if _, ok := data["metric"]; ok {
							if _, ok := data["value"]; ok {
								if _, ok := data["labels"]; ok {
									validEvents++
								}
							}
						}
					}
				}
			}
		}
	}

	if validEvents < 2 {
		t.Errorf("expected at least 2 valid metric_update events, got %d", validEvents)
	}

	t.Logf("Successfully streamed %d metric_update events with %d valid events", metricUpdateCount, validEvents)
}

func TestHTTPServerExportRun(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create a run
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	}
	rec, err := store.Create("test-run", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Set status to running
	_, err = store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, "")
	if err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}

	// Create and store a collector with test data
	collector := metrics.NewCollector()
	collector.Start()

	now := time.Now()
	labels := map[string]string{"service": "svc1", "instance": "svc1-1"}
	collector.Record("cpu_utilization", 0.65, now, labels)
	collector.Record("cpu_utilization", 0.72, now.Add(time.Second), labels)
	collector.Record("memory_utilization", 0.45, now, labels)

	collector.Stop()

	if err := store.SetCollector(rec.Run.Id, collector); err != nil {
		t.Fatalf("SetCollector error: %v", err)
	}

	// Set metrics
	pbMetrics := &simulationv1.RunMetrics{
		TotalRequests:      100,
		SuccessfulRequests: 95,
		FailedRequests:     5,
		LatencyP95Ms:       150.5,
		ThroughputRps:      10.0,
	}
	if err := store.SetMetrics(rec.Run.Id, pbMetrics); err != nil {
		t.Fatalf("SetMetrics error: %v", err)
	}

	// Test export endpoint
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/export", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var export map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &export); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	// Check run data
	runData, ok := export["run"].(map[string]any)
	if !ok {
		t.Fatalf("expected run data")
	}
	if runData["id"] != "test-run" {
		t.Fatalf("expected run id test-run, got %v", runData["id"])
	}

	// Check input data
	inputData, ok := export["input"].(map[string]any)
	if !ok {
		t.Fatalf("expected input data")
	}
	if inputData["scenario_yaml"] != testScenarioYAML {
		t.Fatalf("expected scenario yaml")
	}

	// Check metrics
	metricsData, ok := export["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("expected metrics data")
	}
	if metricsData["total_requests"].(float64) != 100 {
		t.Fatalf("expected total_requests 100, got %v", metricsData["total_requests"])
	}

	// Check time-series data
	timeSeriesData, ok := export["time_series"].([]any)
	if !ok {
		t.Fatalf("expected time_series data")
	}
	if len(timeSeriesData) == 0 {
		t.Fatalf("expected time-series data")
	}
}

func TestHTTPServerExportRunNotFound(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/nonexistent/export", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestHTTPServerExportRunWithoutCollector(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	// Create a run without collector
	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	}
	rec, err := store.Create("test-run", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Set metrics
	pbMetrics := &simulationv1.RunMetrics{
		TotalRequests: 50,
	}
	if err := store.SetMetrics(rec.Run.Id, pbMetrics); err != nil {
		t.Fatalf("SetMetrics error: %v", err)
	}

	// Test export (should work without collector, just no time-series)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/export", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var export map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &export); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	// Should have run, input, and metrics, but no time_series
	if _, ok := export["run"]; !ok {
		t.Fatalf("expected run data")
	}
	if _, ok := export["input"]; !ok {
		t.Fatalf("expected input data")
	}
	if _, ok := export["metrics"]; !ok {
		t.Fatalf("expected metrics data")
	}
	// time_series should not be present
	if _, ok := export["time_series"]; ok {
		t.Fatalf("expected no time_series data when collector not available")
	}
}

func TestHTTPServerExportRunWithOptimizationHistory(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	_, err := store.Create("opt-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if err := store.SetMetrics("opt-run", &simulationv1.RunMetrics{TotalRequests: 10}); err != nil {
		t.Fatalf("SetMetrics error: %v", err)
	}

	step := &simulationv1.OptimizationStep{
		IterationIndex: 1,
		TargetP95Ms:    100,
		ScoreP95Ms:     120,
		Reason:         "p95 above target, scaled replicas up",
		PreviousConfig: &simulationv1.RunConfiguration{
			Services: []*simulationv1.ServiceConfigEntry{{ServiceId: "svc1", Replicas: 2}},
		},
		CurrentConfig: &simulationv1.RunConfiguration{
			Services: []*simulationv1.ServiceConfigEntry{{ServiceId: "svc1", Replicas: 3}},
		},
	}
	if err := store.AppendOptimizationStep("opt-run", step); err != nil {
		t.Fatalf("AppendOptimizationStep error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/opt-run/export", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var export map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &export); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	runData, ok := export["run"].(map[string]any)
	if !ok {
		t.Fatalf("expected run data")
	}
	history, ok := runData["optimization_history"].([]any)
	if !ok || len(history) != 1 {
		t.Fatalf("expected optimization_history with 1 step, got %T %v", runData["optimization_history"], runData["optimization_history"])
	}
	stepData, ok := history[0].(map[string]any)
	if !ok {
		t.Fatalf("expected step as map")
	}
	if stepData["iteration_index"].(float64) != 1 || stepData["reason"] != "p95 above target, scaled replicas up" {
		t.Fatalf("unexpected step data: %+v", stepData)
	}

	// Export should include top-level final_config (last step's current_config)
	finalConfig, ok := export["final_config"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level final_config, got %T %v", export["final_config"], export["final_config"])
	}
	services, ok := finalConfig["services"].([]any)
	if !ok || len(services) != 1 {
		t.Fatalf("expected final_config.services with 1 entry, got %v", finalConfig["services"])
	}
	s0, ok := services[0].(map[string]any)
	if !ok {
		t.Fatalf("expected service as map")
	}
	if s0["service_id"] != "svc1" || s0["replicas"].(float64) != 3 {
		t.Errorf("expected final_config.services[0] service_id svc1 replicas 3, got %v", s0)
	}
}

func TestHTTPServerExportRunPrefersFinalConfigOverOptimizationHistory(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	_, err := store.Create("export-pref-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if err := store.SetMetrics("export-pref-run", &simulationv1.RunMetrics{TotalRequests: 10}); err != nil {
		t.Fatalf("SetMetrics error: %v", err)
	}

	step := &simulationv1.OptimizationStep{
		IterationIndex: 1,
		CurrentConfig: &simulationv1.RunConfiguration{
			Services: []*simulationv1.ServiceConfigEntry{{ServiceId: "svc1", Replicas: 3}},
		},
	}
	if err := store.AppendOptimizationStep("export-pref-run", step); err != nil {
		t.Fatalf("AppendOptimizationStep error: %v", err)
	}
	if err := store.SetFinalConfiguration("export-pref-run", &simulationv1.RunConfiguration{
		Services: []*simulationv1.ServiceConfigEntry{{ServiceId: "svc1", Replicas: 1}},
		Placements: []*simulationv1.InstancePlacementEntry{
			{InstanceId: "from-final-config", ServiceId: "svc1", HostId: "host-1", Lifecycle: "ACTIVE"},
		},
	}); err != nil {
		t.Fatalf("SetFinalConfiguration error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/export-pref-run/export", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var export map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &export); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	finalConfig, ok := export["final_config"].(map[string]any)
	if !ok {
		t.Fatalf("expected top-level final_config, got %T", export["final_config"])
	}
	services, ok := finalConfig["services"].([]any)
	if !ok || len(services) != 1 {
		t.Fatalf("expected final_config.services with 1 entry, got %v", finalConfig["services"])
	}
	s0 := services[0].(map[string]any)
	if s0["replicas"].(float64) != 1 {
		t.Fatalf("expected final_config from RunRecord.FinalConfig (replicas 1), not last optimization step (3), got %v", s0["replicas"])
	}
	pl, ok := finalConfig["placements"].([]any)
	if !ok || len(pl) != 1 {
		t.Fatalf("expected final_config.placements from FinalConfig, got %v", finalConfig["placements"])
	}
	p0 := pl[0].(map[string]any)
	if p0["instance_id"] != "from-final-config" {
		t.Fatalf("expected placement instance_id from FinalConfig, got %v", p0["instance_id"])
	}
}

func TestHTTPServerListRuns(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	// Create some test runs
	for i := 0; i < 5; i++ {
		_, err := store.Create("", &simulationv1.RunInput{ScenarioYaml: "test"})
		if err != nil {
			t.Fatalf("Create error: %v", err)
		}
	}

	// Test GET /v1/runs (default)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	runs, ok := body["runs"].([]any)
	if !ok {
		t.Fatalf("expected runs array")
	}
	if len(runs) == 0 {
		t.Fatalf("expected at least one run")
	}

	pagination, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("expected pagination object")
	}
	if pagination["limit"] == nil {
		t.Fatalf("expected limit in pagination")
	}
	if pagination["offset"] == nil {
		t.Fatalf("expected offset in pagination")
	}
	if pagination["count"] == nil {
		t.Fatalf("expected count in pagination")
	}
}

func TestHTTPServerListRunsWithPagination(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	// Create 10 test runs
	for i := 0; i < 10; i++ {
		_, err := store.Create("", &simulationv1.RunInput{ScenarioYaml: "test"})
		if err != nil {
			t.Fatalf("Create error: %v", err)
		}
	}

	// Test with limit
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs?limit=3", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	runs, ok := body["runs"].([]any)
	if !ok {
		t.Fatalf("expected runs array")
	}
	if len(runs) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(runs))
	}

	pagination, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("expected pagination object")
	}
	if pagination["limit"].(float64) != 3 {
		t.Fatalf("expected limit 3, got %v", pagination["limit"])
	}

	// Test with offset
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs?limit=3&offset=3", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	runs, ok = body["runs"].([]any)
	if !ok {
		t.Fatalf("expected runs array")
	}
	if len(runs) != 3 {
		t.Fatalf("expected 3 runs with offset, got %d", len(runs))
	}

	pagination, ok = body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("expected pagination object")
	}
	if pagination["offset"].(float64) != 3 {
		t.Fatalf("expected offset 3, got %v", pagination["offset"])
	}
}

func TestHTTPServerListRunsWithStatusFilter(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	// Create runs with different statuses
	rec1, _ := store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "test"})
	store.SetStatus("run-1", simulationv1.RunStatus_RUN_STATUS_COMPLETED, "")

	_, _ = store.Create("run-2", &simulationv1.RunInput{ScenarioYaml: "test"})
	store.SetStatus("run-2", simulationv1.RunStatus_RUN_STATUS_RUNNING, "")

	_, _ = store.Create("run-3", &simulationv1.RunInput{ScenarioYaml: "test"})
	store.SetStatus("run-3", simulationv1.RunStatus_RUN_STATUS_PENDING, "")

	// Test filter by COMPLETED
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs?status=completed", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	runs, ok := body["runs"].([]any)
	if !ok {
		t.Fatalf("expected runs array")
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 completed run, got %d", len(runs))
	}

	run, ok := runs[0].(map[string]any)
	if !ok {
		t.Fatalf("expected run object")
	}
	if run["id"].(string) != rec1.Run.Id {
		t.Fatalf("expected run-1, got %v", run["id"])
	}
}

func TestHTTPServerUpdateWorkloadRate(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create and start a run - use real-time mode so sim runs ~300ms real time (discrete-event completes in microseconds)
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   300,
		RealTimeMode: true,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	_, err = executor.Start(rec.Run.Id)
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Brief delay to let workload state initialize
	time.Sleep(50 * time.Millisecond)

	// Check if run has already completed before attempting update (safety for slow CI)
	updatedRec, _ := store.Get(rec.Run.Id)
	if updatedRec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Skipf("Simulation completed too quickly (status: %v) - skipping rate update test", updatedRec.Run.Status)
	}

	// Test successful rate update
	reqBody := map[string]any{
		"pattern_key": "client:svc1:/test",
		"rate_rps":    50.0,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["message"] != "workload updated successfully" {
		t.Fatalf("expected success message, got %v", resp["message"])
	}

	// Stop the run if it's still running
	_, _ = executor.Stop(rec.Run.Id)
}

func TestHTTPServerUpdateWorkloadPattern(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create and start a run - use real-time mode so sim runs ~300ms real time (discrete-event completes in microseconds)
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   300,
		RealTimeMode: true,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	_, err = executor.Start(rec.Run.Id)
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Brief delay to let workload state initialize
	time.Sleep(50 * time.Millisecond)

	// Check if run has already completed before attempting update (safety for slow CI)
	updatedRec, _ := store.Get(rec.Run.Id)
	if updatedRec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Skipf("Simulation completed too quickly (status: %v) - skipping pattern update test", updatedRec.Run.Status)
	}

	// Test successful pattern update
	reqBody := map[string]any{
		"pattern_key": "client:svc1:/test",
		"pattern": map[string]any{
			"from": "client",
			"to":   "svc1:/test",
			"arrival": map[string]any{
				"type":     "poisson",
				"rate_rps": 75.0,
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["message"] != "workload updated successfully" {
		t.Fatalf("expected success message, got %v", resp["message"])
	}

	// Stop the run if it's still running
	_, _ = executor.Stop(rec.Run.Id)
}

func TestHTTPServerUpdateWorkloadValidation(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	// Create a run for validation tests (but don't start it)
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Test missing pattern_key
	reqBody := map[string]any{
		"rate_rps": 50.0,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}

	// Test negative rate
	reqBody = map[string]any{
		"pattern_key": "client:svc1:/test",
		"rate_rps":    -5.0,
	}
	bodyBytes, _ = json.Marshal(reqBody)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for negative rate, got %d", rr.Code)
	}

	// Test zero rate
	reqBody = map[string]any{
		"pattern_key": "client:svc1:/test",
		"rate_rps":    0.0,
	}
	bodyBytes, _ = json.Marshal(reqBody)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for zero rate, got %d", rr.Code)
	}

	// Test neither rate_rps nor pattern provided
	reqBody = map[string]any{
		"pattern_key": "client:svc1:/test",
	}
	bodyBytes, _ = json.Marshal(reqBody)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 when neither rate_rps nor pattern provided, got %d", rr.Code)
	}

	// Test non-existent run
	reqBody = map[string]any{
		"pattern_key": "client:svc1:/test",
		"rate_rps":    50.0,
	}
	bodyBytes, _ = json.Marshal(reqBody)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/v1/runs/non-existent/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for non-existent run, got %d", rr.Code)
	}
}

func TestHTTPServerUpdateWorkloadNotRunning(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	// Create but don't start
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Try to update when run is not running
	reqBody := map[string]any{
		"pattern_key": "client:svc1:/test",
		"rate_rps":    50.0,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 when updating non-running run, got %d", rr.Code)
	}
}

func TestHTTPServerRenewOnlineLease(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	rec, err := store.Create("http-lease-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
		Optimization: &simulationv1.OptimizationConfig{
			Online:             true,
			TargetP95LatencyMs: 50,
			LeaseTtlMs:         60_000,
		},
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+rec.Run.Id+"/online/renew-lease", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	run, ok := resp["run"].(map[string]any)
	if !ok || run["id"] != rec.Run.Id {
		t.Fatalf("expected run with id %q in response, got %#v", rec.Run.Id, resp["run"])
	}
}

func TestHTTPServerRenewOnlineLeaseNotFound(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/does-not-exist/online/renew-lease", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPServerRenewOnlineLeaseLeaseNotConfigured(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	rec, err := store.Create("http-no-lease-ttl", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
		Optimization: &simulationv1.OptimizationConfig{
			Online:               true,
			TargetP95LatencyMs:   50,
			AllowUnboundedOnline: true,
			MaxOnlineDurationMs:  0,
		},
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+rec.Run.Id+"/online/renew-lease", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestConvertMetricsToJSONIncludesQueueAndErrorTaxonomy(t *testing.T) {
	pb := &simulationv1.RunMetrics{
		TotalRequests:         10,
		IngressRequests:       4,
		IngressFailedRequests: 1,
		IngressErrorRate:      0.25,
		AttemptFailedRequests: 3,
		AttemptErrorRate:      0.3,
		RetryAttempts:         2,
		TimeoutErrors:         1,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{
				ServiceName:             "svc1",
				QueueWaitP50Ms:          1,
				ProcessingLatencyP50Ms:  9,
				ProcessingLatencyMeanMs: 10,
			},
		},
	}
	j := convertMetricsToJSON(pb)
	if j["ingress_error_rate"].(float64) != 0.25 {
		t.Fatalf("json ingress_error_rate: %v", j["ingress_error_rate"])
	}
	sm := j["service_metrics"].([]map[string]any)[0]
	if sm["queue_wait_p50_ms"].(float64) != 1 {
		t.Fatalf("json queue_wait_p50_ms: %v", sm["queue_wait_p50_ms"])
	}
	if sm["processing_latency_mean_ms"].(float64) != 10 {
		t.Fatalf("json processing_latency_mean_ms: %v", sm["processing_latency_mean_ms"])
	}
}
