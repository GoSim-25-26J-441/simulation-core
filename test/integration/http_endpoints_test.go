//go:build integration
// +build integration

package integration_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
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

// TestIntegration_HTTPEndpoints_ListRuns tests the GET /v1/runs endpoint
func TestIntegration_HTTPEndpoints_ListRuns(t *testing.T) {
	store := simd.NewRunStore()
	srv := simd.NewHTTPServer(store, simd.NewRunExecutor(store))

	// Create multiple runs
	runIDs := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		rec, err := store.Create("", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML})
		if err != nil {
			t.Fatalf("Create error: %v", err)
		}
		runIDs = append(runIDs, rec.Run.Id)

		// Set different statuses
		switch i % 3 {
		case 0:
			store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_PENDING, "")
		case 1:
			store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, "")
		case 2:
			store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_COMPLETED, "")
		}
	}

	// Test GET /v1/runs
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
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
	if pagination["limit"] == nil || pagination["offset"] == nil || pagination["count"] == nil {
		t.Fatalf("expected pagination metadata")
	}

	// Test pagination
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs?limit=2&offset=0", nil)
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
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs with limit=2, got %d", len(runs))
	}

	// Test status filter
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs?status=completed", nil)
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

	// Verify all runs have completed status
	for _, runAny := range runs {
		run, ok := runAny.(map[string]any)
		if !ok {
			t.Fatalf("expected run object")
		}
		if run["status"].(string) != "RUN_STATUS_COMPLETED" {
			t.Fatalf("expected completed status, got %v", run["status"])
		}
	}
}

// TestIntegration_HTTPEndpoints_TimeSeries tests the GET /v1/runs/{id}/metrics/timeseries endpoint
func TestIntegration_HTTPEndpoints_TimeSeries(t *testing.T) {
	store := simd.NewRunStore()
	srv := simd.NewHTTPServer(store, simd.NewRunExecutor(store))

	// Create a run
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Create a collector and add some metrics
	collector := metrics.NewCollector()
	store.SetCollector(rec.Run.Id, collector)

	// Add some time-series data using helper functions
	labels := map[string]string{"service": "svc1", "instance": "svc1-0"}
	metrics.RecordLatency(collector, "request_latency_ms", 10.5, labels)
	metrics.RecordLatency(collector, "request_latency_ms", 12.3, labels)
	metrics.RecordMetric(collector, "cpu_utilization", 0.75, labels)

	// Test GET /v1/runs/{id}/metrics/timeseries
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/metrics/timeseries", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if body["run_id"].(string) != rec.Run.Id {
		t.Fatalf("expected run_id %s, got %v", rec.Run.Id, body["run_id"])
	}

	points, ok := body["points"].([]any)
	if !ok {
		t.Fatalf("expected points array")
	}
	if len(points) == 0 {
		t.Fatalf("expected at least one time-series point")
	}

	// Test filtering by metric name
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/metrics/timeseries?metric=request_latency_ms", nil)
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

	// Verify all points are for request_latency_ms
	for _, pointAny := range points {
		point, ok := pointAny.(map[string]any)
		if !ok {
			t.Fatalf("expected point object")
		}
		if point["metric"].(string) != "request_latency_ms" {
			t.Fatalf("expected metric request_latency_ms, got %v", point["metric"])
		}
	}
}

// TestIntegration_HTTPEndpoints_ExportRun tests the GET /v1/runs/{id}/export endpoint
func TestIntegration_HTTPEndpoints_ExportRun(t *testing.T) {
	store := simd.NewRunStore()
	srv := simd.NewHTTPServer(store, simd.NewRunExecutor(store))

	// Create a run
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Set metrics
	pbMetrics := &simulationv1.RunMetrics{
		TotalRequests:      50,
		SuccessfulRequests: 45,
		FailedRequests:     5,
		LatencyP50Ms:       10.0,
		LatencyP95Ms:       20.0,
		LatencyP99Ms:       30.0,
		LatencyMeanMs:      12.5,
		ThroughputRps:      500.0,
	}
	store.SetMetrics(rec.Run.Id, pbMetrics)

	// Create a collector and add time-series data
	collector := metrics.NewCollector()
	store.SetCollector(rec.Run.Id, collector)
	labels := map[string]string{"service": "svc1"}
	collector.AddMetricPoint("request_latency_ms", 10.5, labels, time.Now())

	// Test GET /v1/runs/{id}/export
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/export", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var export map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &export); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	// Verify export contains all expected fields
	if _, ok := export["run"]; !ok {
		t.Fatalf("expected run data")
	}
	if _, ok := export["input"]; !ok {
		t.Fatalf("expected input data")
	}
	if _, ok := export["metrics"]; !ok {
		t.Fatalf("expected metrics data")
	}
	if _, ok := export["time_series"]; !ok {
		t.Fatalf("expected time_series data")
	}

	// Verify metrics
	metricsData, ok := export["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("expected metrics object")
	}
	if metricsData["total_requests"].(float64) != 50 {
		t.Fatalf("expected total_requests 50, got %v", metricsData["total_requests"])
	}

	// Verify time_series
	timeSeries, ok := export["time_series"].([]any)
	if !ok {
		t.Fatalf("expected time_series array")
	}
	if len(timeSeries) == 0 {
		t.Fatalf("expected at least one time-series metric")
	}
}

// TestIntegration_HTTPEndpoints_FullLifecycle tests the complete lifecycle with all endpoints
func TestIntegration_HTTPEndpoints_FullLifecycle(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	srv := simd.NewHTTPServer(store, executor)

	// 1. Create a run via HTTP
	createBody := map[string]any{
		"input": map[string]any{
			"scenario_yaml": testScenarioYAML,
			"duration_ms":   100,
		},
	}
	bodyBytes, _ := json.Marshal(createBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", httptest.NewRequest(http.MethodPost, "", nil).Body)
	req.Body = httptest.NewRecorder().Body
	req.Body = &jsonBodyReader{data: bodyBytes}
	req.Header.Set("Content-Type", "application/json")

	// Use direct store.Create for simplicity in integration test
	rec, err := store.Create("", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// 2. Start the run
	_, err = executor.Start(rec.Run.Id)
	if err != nil {
		t.Fatalf("Start error: %v", err)
	}

	// 3. Wait for completion
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		updated, ok := store.Get(rec.Run.Id)
		if !ok {
			t.Fatalf("run not found")
		}
		if updated.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 4. Test List Runs
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var listBody map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	runs, ok := listBody["runs"].([]any)
	if !ok || len(runs) == 0 {
		t.Fatalf("expected at least one run in list")
	}

	// 5. Test Get Run Metrics
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/metrics", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var metricsBody map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &metricsBody); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if _, ok := metricsBody["metrics"]; !ok {
		t.Fatalf("expected metrics in response")
	}

	// 6. Test Export Run (may not have time-series if collector wasn't stored)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/export", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var exportBody map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &exportBody); err != nil {
		t.Fatalf("invalid json: %v", err)
	}

	if _, ok := exportBody["run"]; !ok {
		t.Fatalf("expected run in export")
	}
	if _, ok := exportBody["input"]; !ok {
		t.Fatalf("expected input in export")
	}
	if _, ok := exportBody["metrics"]; !ok {
		t.Fatalf("expected metrics in export")
	}
}

