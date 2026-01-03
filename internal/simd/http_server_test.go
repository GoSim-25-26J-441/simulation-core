package simd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
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
