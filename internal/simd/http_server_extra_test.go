package simd

import (
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

func TestHTTPServerHandleRunsMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Test PUT /v1/runs (should be method not allowed)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/runs", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Create a run first
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Test PUT /v1/runs/{id} (should be method not allowed)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/v1/runs/"+rec.Run.Id, nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDStopMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Create a run first
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Test GET /v1/runs/{id}:stop (should be method not allowed)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+":stop", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDMetricsMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Create a run first
	rec, err := store.Create("test-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	// Test POST /v1/runs/{id}/metrics (should be method not allowed)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+rec.Run.Id+"/metrics", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDEmptyPath(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Test /v1/runs/ (empty run ID)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rr.Code)
	}
}

func TestHTTPServerWriteError(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	srv.writeError(rr, http.StatusInternalServerError, "test error")

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rr.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["error"] != "test error" {
		t.Fatalf("expected error 'test error', got %s", body["error"])
	}
}

func TestHTTPServerWriteJSON(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	testData := map[string]string{"key": "value"}
	srv.writeJSON(rr, http.StatusOK, testData)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["key"] != "value" {
		t.Fatalf("expected key 'value', got %s", body["key"])
	}
}

func TestHTTPServerCreateRunInvalidJSON(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(`{invalid json`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPServerCreateRunInputRequired(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(`{"run_id": "test"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPServerListRunsLimitCap(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs?limit=2000", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	pag, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatalf("expected pagination")
	}
	if pLimit, ok := pag["limit"].(float64); !ok || pLimit != 1000 {
		t.Fatalf("expected limit capped at 1000, got %v", pag["limit"])
	}
}

func TestHTTPServerListRunsStatusFilter(t *testing.T) {
	store := NewRunStore()
	_, _ = store.Create("run-1", &simulationv1.RunInput{ScenarioYaml: "hosts: []"})
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs?status=cancelled", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
}

func TestHTTPServerListRunsWithOffset(t *testing.T) {
	store := NewRunStore()
	for i := 0; i < 5; i++ {
		_, _ = store.Create("", &simulationv1.RunInput{ScenarioYaml: "hosts: []"})
	}
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs?limit=2&offset=2", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	runs, ok := body["runs"].([]any)
	if !ok {
		t.Fatalf("expected runs array")
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs with limit=2 offset=2, got %d", len(runs))
	}
	pag := body["pagination"].(map[string]any)
	if pag["offset"].(float64) != 2 {
		t.Fatalf("expected offset 2")
	}
}

func TestHTTPServerHandleRunByIDExportMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	rec, _ := store.Create("test-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML})
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+rec.Run.Id+"/export", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDMetricsStreamMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	rec, _ := store.Create("test-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML})
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+rec.Run.Id+"/metrics/stream", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDTimeseriesMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	rec, _ := store.Create("test-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML})
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs/"+rec.Run.Id+"/metrics/timeseries", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerHandleRunByIDWorkloadMethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	rec, _ := store.Create("test-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML})
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/workload", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d", rr.Code)
	}
}

func TestHTTPServerTimeSeriesInvalidStartTime(t *testing.T) {
	store := NewRunStore()
	rec, _ := store.Create("test-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML})
	collector := metrics.NewCollector()
	collector.Start()
	collector.Stop()
	_ = store.SetCollector(rec.Run.Id, collector)
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/timeseries?start_time=invalid", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPServerTimeSeriesInvalidEndTime(t *testing.T) {
	store := NewRunStore()
	rec, _ := store.Create("test-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML})
	collector := metrics.NewCollector()
	collector.Start()
	collector.Stop()
	_ = store.SetCollector(rec.Run.Id, collector)
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/timeseries?end_time=bad-time", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPServerGetRunMetricsWithServiceMetrics(t *testing.T) {
	store := NewRunStore()
	rec, _ := store.Create("test-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML})
	metrics := &simulationv1.RunMetrics{
		TotalRequests: 100,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{ServiceName: "svc1", RequestCount: 50, CpuUtilization: 0.5},
		},
	}
	_ = store.SetMetrics(rec.Run.Id, metrics)
	_, _ = store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_COMPLETED, "")
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	metricsResp, ok := body["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("expected metrics object")
	}
	svcMetrics, ok := metricsResp["service_metrics"].([]any)
	if !ok || len(svcMetrics) == 0 {
		t.Fatalf("expected service_metrics array")
	}
}

func TestHTTPServerTimeSeriesWithValidTimeFormats(t *testing.T) {
	store := NewRunStore()
	rec, _ := store.Create("test-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML})
	collector := metrics.NewCollector()
	collector.Start()
	now := time.Now()
	collector.Record("cpu_utilization", 0.5, now, map[string]string{"service": "svc1"})
	collector.Stop()
	_ = store.SetCollector(rec.Run.Id, collector)
	srv := NewHTTPServer(store, NewRunExecutor(store))

	// Test with Unix milliseconds
	startMs := now.Add(-time.Minute).UnixMilli()
	endMs := now.Add(time.Minute).UnixMilli()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/test-run/metrics/timeseries?start_time="+strconv.FormatInt(startMs, 10)+"&end_time="+strconv.FormatInt(endMs, 10), nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 with Unix ms times, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHTTPServerGetRunWithOptimizationResult(t *testing.T) {
	store := NewRunStore()
	rec, _ := store.Create("opt-run", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML})
	_, _ = store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_COMPLETED, "")
	_ = store.SetOptimizationResult(rec.Run.Id, "best-123", 42.5, 10)
	srv := NewHTTPServer(store, NewRunExecutor(store))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/opt-run", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	run, ok := body["run"].(map[string]any)
	if !ok {
		t.Fatalf("expected run object")
	}
	if run["best_run_id"] != "best-123" {
		t.Fatalf("expected best_run_id best-123, got %v", run["best_run_id"])
	}
}
