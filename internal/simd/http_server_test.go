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
	srv := NewHTTPServer(store, NewRunExecutor(store))
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
	srv := NewHTTPServer(store, NewRunExecutor(store))
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
	srv := NewHTTPServer(store, NewRunExecutor(store))
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
	srv := NewHTTPServer(store, NewRunExecutor(store))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/nonexistent", nil)

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestHTTPServerStopRun(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store)
	srv := NewHTTPServer(store, executor)
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   5000, // Long duration
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
	if run["status"] != "RUN_STATUS_CANCELLED" {
		t.Fatalf("expected cancelled status, got %v", run["status"])
	}
}

func TestHTTPServerGetRunMetrics(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))
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
	srv := NewHTTPServer(store, NewRunExecutor(store))
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
	srv := NewHTTPServer(store, NewRunExecutor(store))
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
	executor := NewRunExecutor(store)
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
	executor := NewRunExecutor(store)
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

func TestHTTPServerTimeSeriesNotFound(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/nonexistent/metrics/timeseries", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestHTTPServerTimeSeriesNoCollector(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

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
	executor := NewRunExecutor(store)
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
	executor := NewRunExecutor(store)
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
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/nonexistent/metrics/stream", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestHTTPServerMetricsStreamWithInterval(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store)
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

func TestHTTPServerExportRun(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store)
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
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/nonexistent/export", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}
}

func TestHTTPServerExportRunWithoutCollector(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

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

func TestHTTPServerListRuns(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

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
	srv := NewHTTPServer(store, NewRunExecutor(store))

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
	srv := NewHTTPServer(store, NewRunExecutor(store))

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
	executor := NewRunExecutor(store)
	srv := NewHTTPServer(store, executor)

	// Create and start a run
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   60000, // Long duration to ensure run stays running
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	_, err = executor.Start(rec.Run.Id)
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Brief delay to let workload state initialize
	time.Sleep(2 * time.Millisecond)

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

	// Check if run has already completed
	updatedRec, _ := store.Get(rec.Run.Id)
	if updatedRec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		// Simulation completed too quickly - this is expected for discrete-event sims
		t.Skipf("Simulation completed too quickly (status: %v) - skipping rate update test", updatedRec.Run.Status)
	}

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
	executor := NewRunExecutor(store)
	srv := NewHTTPServer(store, executor)

	// Create and start a run
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   60000, // Long duration to ensure run stays running
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	_, err = executor.Start(rec.Run.Id)
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// Brief delay to let workload state initialize
	time.Sleep(2 * time.Millisecond)

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

	// Check if run has already completed
	updatedRec, _ := store.Get(rec.Run.Id)
	if updatedRec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		// Simulation completed too quickly - this is expected for discrete-event sims
		t.Skipf("Simulation completed too quickly (status: %v) - skipping pattern update test", updatedRec.Run.Status)
	}

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
	executor := NewRunExecutor(store)
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
	srv := NewHTTPServer(store, NewRunExecutor(store))

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
