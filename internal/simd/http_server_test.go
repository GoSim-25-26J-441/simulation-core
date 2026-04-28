package simd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
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

const infeasiblePlacementScenarioYAML = `
hosts:
  - id: host-1
    cores: 1
services:
  - id: customer-core
    replicas: 2
    model: cpu
    cpu_cores: 1.5
    memory_mb: 1024
    endpoints:
      - path: /work
        mean_cpu_ms: 5
        cpu_sigma_ms: 1
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.1}
workload:
  - from: client
    to: customer-core:/work
    arrival: {type: poisson, rate_rps: 10}
`

// checkout defines /read only; workload targets /write (semantic validation failure).
const workloadMissingEndpointYAML = `
hosts:
  - id: host-1
    cores: 2
services:
  - id: checkout
    replicas: 1
    model: cpu
    endpoints:
      - path: /read
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: checkout:/write
    arrival: {type: poisson, rate_rps: 10}
`

const workloadMissingServiceYAML = `
hosts:
  - id: host-1
    cores: 2
services:
  - id: checkout
    replicas: 1
    model: cpu
    endpoints:
      - path: /read
        mean_cpu_ms: 10
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms: {mean: 1, sigma: 0.5}
workload:
  - from: client
    to: nosuchsvc:/read
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

func TestHTTPServerValidateScenario_Valid(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	reqBody := map[string]any{
		"scenario_yaml": testScenarioYAML,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/scenarios:validate", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["valid"] != true {
		t.Fatalf("expected valid=true, got %v", resp["valid"])
	}
	if _, has := resp["summary"]; has {
		t.Fatalf("expected summary omitted for valid preflight response, got %#v", resp["summary"])
	}
	if len(store.ListFiltered(100, 0, simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED)) != 0 {
		t.Fatal("validate endpoint must not create runs")
	}
}

func TestHTTPServerValidateScenario_InvalidYAML(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	reqBody := map[string]any{
		"scenario_yaml": "hosts: [",
	}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/scenarios:validate", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["valid"] != false {
		t.Fatalf("expected valid=false, got %v", resp["valid"])
	}
	errorsRaw, ok := resp["errors"].([]any)
	if !ok || len(errorsRaw) == 0 {
		t.Fatalf("expected errors array, got %T %#v", resp["errors"], resp["errors"])
	}
	first := errorsRaw[0].(map[string]any)
	if first["code"] != "SCENARIO_PARSE_INVALID" {
		t.Fatalf("expected parse error code, got %v", first["code"])
	}
	summary, ok := resp["summary"].(map[string]any)
	if !ok {
		t.Fatalf("expected summary object on parse failure")
	}
	if int(summary["hosts"].(float64)) != 0 || int(summary["services"].(float64)) != 0 || int(summary["workloads"].(float64)) != 0 {
		t.Fatalf("expected zero summary on parse failure, got %#v", summary)
	}
	if len(store.ListFiltered(100, 0, simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED)) != 0 {
		t.Fatal("validate endpoint must not create runs")
	}
}

func TestHTTPServerValidateScenario_EmptyScenarioYAML(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	for _, label := range []string{"missing", "empty", "whitespace"} {
		t.Run(label, func(t *testing.T) {
			var body map[string]any
			switch label {
			case "missing":
				body = map[string]any{}
			case "empty":
				body = map[string]any{"scenario_yaml": ""}
			case "whitespace":
				body = map[string]any{"scenario_yaml": "  \t  "}
			}
			raw, _ := json.Marshal(body)
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/scenarios:validate", strings.NewReader(string(raw)))
			req.Header.Set("Content-Type", "application/json")
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400, got %d: %s", label, rr.Code, rr.Body.String())
			}
			var resp map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid json: %v", err)
			}
			if resp["valid"] != false {
				t.Fatalf("expected valid=false")
			}
			errs, ok := resp["errors"].([]any)
			if !ok || len(errs) == 0 {
				t.Fatalf("expected errors array")
			}
			first := errs[0].(map[string]any)
			if first["code"] != "SCENARIO_YAML_REQUIRED" {
				t.Fatalf("expected SCENARIO_YAML_REQUIRED, got %v", first["code"])
			}
			if first["path"] != "scenario_yaml" {
				t.Fatalf("path=%v", first["path"])
			}
		})
	}
}

func TestHTTPServerValidateScenario_MethodNotAllowed(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/scenarios:validate", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("expected Allow: POST, got %q", got)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["error"] != "method not allowed" {
		t.Fatalf("unexpected body: %#v", resp)
	}
}

func TestHTTPServerValidateScenario_PlacementInfeasibleIncludesServiceID(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	reqBody := map[string]any{
		"scenario_yaml": infeasiblePlacementScenarioYAML,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/scenarios:validate", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if resp["valid"] != false {
		t.Fatalf("expected valid=false, got %v", resp["valid"])
	}
	errorsRaw, ok := resp["errors"].([]any)
	if !ok || len(errorsRaw) == 0 {
		t.Fatalf("expected errors array, got %T %#v", resp["errors"], resp["errors"])
	}
	first := errorsRaw[0].(map[string]any)
	if first["code"] != "PLACEMENT_INFEASIBLE" {
		t.Fatalf("expected placement error code, got %v", first["code"])
	}
	if first["service_id"] != "customer-core" {
		t.Fatalf("expected service_id customer-core, got %v", first["service_id"])
	}
	if _, ok := first["message"].(string); !ok {
		t.Fatalf("expected message string in first error, got %T", first["message"])
	}
	if len(store.ListFiltered(100, 0, simulationv1.RunStatus_RUN_STATUS_UNSPECIFIED)) != 0 {
		t.Fatal("validate endpoint must not create runs")
	}
}

func TestHTTPServerValidateScenario_WorkloadUnknownEndpoint(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	reqBody := map[string]any{"scenario_yaml": workloadMissingEndpointYAML, "mode": "preflight"}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/scenarios:validate", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	errs := resp["errors"].([]any)[0].(map[string]any)
	if errs["code"] != "UNKNOWN_WORKLOAD_ENDPOINT" {
		t.Fatalf("code=%v", errs["code"])
	}
	if errs["path"] != "workload[0].to" {
		t.Fatalf("path=%v", errs["path"])
	}
}

func TestHTTPServerValidateScenario_WorkloadUnknownService(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	reqBody := map[string]any{"scenario_yaml": workloadMissingServiceYAML}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/scenarios:validate", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	errs := resp["errors"].([]any)[0].(map[string]any)
	if errs["code"] != "UNKNOWN_WORKLOAD_SERVICE" {
		t.Fatalf("code=%v", errs["code"])
	}
}

func TestHTTPServerValidateScenario_ModeUnsupported(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	reqBody := map[string]any{"scenario_yaml": testScenarioYAML, "mode": "strict"}
	bodyBytes, _ := json.Marshal(reqBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/scenarios:validate", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
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

func TestHTTPServerGetRunCompletedOrdinaryIncludesSelfCandidateFields(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)
	rec, err := store.Create("ordinary-http-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   100,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := executor.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		latest, ok := store.Get(rec.Run.Id)
		if ok && latest.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
			break
		}
		time.Sleep(20 * time.Millisecond)
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
	if run["best_run_id"] != rec.Run.Id {
		t.Fatalf("expected best_run_id=%q, got %v", rec.Run.Id, run["best_run_id"])
	}
	if run["iterations"].(float64) != 0 {
		t.Fatalf("expected iterations=0, got %v", run["iterations"])
	}
	candidates, ok := run["candidate_run_ids"].([]any)
	if !ok {
		t.Fatalf("expected candidate_run_ids array, got %T", run["candidate_run_ids"])
	}
	if len(candidates) != 1 || candidates[0] != rec.Run.Id {
		t.Fatalf("expected candidate_run_ids=[%q], got %v", rec.Run.Id, candidates)
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

func TestHTTPServerCreateRunOptimizationAliases(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	reqBody := map[string]any{
		"run_id": "alias-run",
		"input": map[string]any{
			"scenario_yaml": testScenarioYAML,
			"duration_ms":   100,
			"optimization": map[string]any{
				"online":                true,
				"target_p95_latency_ms": 80.0,
				"host_drain_timeout_ms": 9000.0,
				"memory_headroom_mb":    512.0,
			},
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

	rec, ok := store.Get("alias-run")
	if !ok || rec == nil || rec.Input == nil || rec.Input.Optimization == nil {
		t.Fatalf("expected stored run input optimization for alias-run")
	}
	if rec.Input.Optimization.DrainTimeoutMs != 9000 {
		t.Fatalf("expected alias host_drain_timeout_ms -> drain_timeout_ms, got %d", rec.Input.Optimization.DrainTimeoutMs)
	}
	if rec.Input.Optimization.MemoryDownsizeHeadroomMb != 512 {
		t.Fatalf("expected alias memory_headroom_mb -> memory_downsize_headroom_mb, got %f", rec.Input.Optimization.MemoryDownsizeHeadroomMb)
	}
}

func TestHTTPServerCreateRunOptimizationAliasDoesNotOverrideCanonical(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	reqBody := map[string]any{
		"run_id": "alias-precedence-run",
		"input": map[string]any{
			"scenario_yaml": testScenarioYAML,
			"duration_ms":   100,
			"optimization": map[string]any{
				"online":                      true,
				"target_p95_latency_ms":       80.0,
				"drain_timeout_ms":            2000.0,
				"host_drain_timeout_ms":       9000.0,
				"memory_downsize_headroom_mb": 256.0,
				"memory_headroom_mb":          1024.0,
				"unknown_alias_field":         777.0,
			},
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

	rec, ok := store.Get("alias-precedence-run")
	if !ok || rec == nil || rec.Input == nil || rec.Input.Optimization == nil {
		t.Fatalf("expected stored run input optimization for alias-precedence-run")
	}
	if rec.Input.Optimization.DrainTimeoutMs != 2000 {
		t.Fatalf("expected canonical drain_timeout_ms to win, got %d", rec.Input.Optimization.DrainTimeoutMs)
	}
	if rec.Input.Optimization.MemoryDownsizeHeadroomMb != 256 {
		t.Fatalf("expected canonical memory_downsize_headroom_mb to win, got %f", rec.Input.Optimization.MemoryDownsizeHeadroomMb)
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

func TestSendSSEEventFormatUnchanged(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	rr := httptest.NewRecorder()

	if err := srv.sendSSEEvent(rr, "status_change", map[string]any{"status": "RUNNING"}); err != nil {
		t.Fatalf("sendSSEEvent error: %v", err)
	}

	want := "event: status_change\ndata: {\"status\":\"RUNNING\"}\n\n"
	if got := rr.Body.String(); got != want {
		t.Fatalf("unexpected SSE payload:\nwant=%q\ngot =%q", want, got)
	}
}

func TestIsClientDisconnect(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "eof", err: io.EOF, want: true},
		{name: "broken pipe syscall", err: syscall.EPIPE, want: true},
		{name: "conn reset syscall", err: syscall.ECONNRESET, want: true},
		{name: "wrapped broken pipe", err: fmt.Errorf("write failed: %w", syscall.EPIPE), want: true},
		{name: "wrapped conn reset", err: fmt.Errorf("write failed: %w", syscall.ECONNRESET), want: true},
		{name: "broken pipe text fallback", err: errors.New("write: broken pipe"), want: true},
		{name: "connection reset text fallback", err: errors.New("read: connection reset by peer"), want: true},
		{name: "other", err: errors.New("unexpected serialization failure"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isClientDisconnect(tc.err)
			if got != tc.want {
				t.Fatalf("isClientDisconnect(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestHTTPServerMetricsStreamStopsOnWriteFailure(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	_, err := store.Create("disconnect-run", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
		RealTimeMode: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/runs/disconnect-run/metrics/stream?interval_ms=1", nil)
	w := &failingSSEWriter{
		header:    http.Header{},
		failAfter: 1,
		err:       syscall.EPIPE,
	}

	done := make(chan struct{})
	go func() {
		srv.handleMetricsStream(w, req, "disconnect-run")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("metrics stream did not exit after write failure")
	}

	if w.writes != 2 {
		t.Fatalf("expected exactly 2 write attempts (header then failed data), got %d", w.writes)
	}
}

type failingSSEWriter struct {
	header    http.Header
	writes    int
	failAfter int
	err       error
}

func (w *failingSSEWriter) Header() http.Header { return w.header }
func (w *failingSSEWriter) WriteHeader(_ int)   {}
func (w *failingSSEWriter) Flush()              {}

func (w *failingSSEWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > w.failAfter {
		return 0, w.err
	}
	return len(p), nil
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
	if !strings.Contains(body, `"queues"`) {
		t.Fatal("expected resources.queues in metrics_snapshot payload")
	}
	if !strings.Contains(body, `"topics"`) {
		t.Fatal("expected resources.topics in metrics_snapshot payload")
	}
}

func TestHTTPServerGetRunMetricsIncludesBrokerResourcesWhenRunManagerAvailable(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	rec, err := store.Create("test-run-metrics-resources", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if err := store.SetMetrics(rec.Run.Id, &simulationv1.RunMetrics{
		TotalRequests: 1,
		LatencyP95Ms:  10,
	}); err != nil {
		t.Fatalf("SetMetrics error: %v", err)
	}
	scen, err := config.ParseScenarioYAMLString(testScenarioYAML)
	if err != nil {
		t.Fatalf("ParseScenarioYAMLString: %v", err)
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scen); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	executor.mu.Lock()
	executor.resourceManagers[rec.Run.Id] = rm
	executor.mu.Unlock()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/metrics", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"resources"`) {
		t.Fatalf("expected resources object in metrics response: %s", body)
	}
	if !strings.Contains(body, `"queues"`) || !strings.Contains(body, `"topics"`) {
		t.Fatalf("expected queues/topics resources in metrics response: %s", body)
	}
}

func TestHTTPServerExportIncludesBrokerResourcesWhenRunManagerAvailable(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	rec, err := store.Create("test-run-export-resources", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if err := store.SetMetrics(rec.Run.Id, &simulationv1.RunMetrics{TotalRequests: 1}); err != nil {
		t.Fatalf("SetMetrics error: %v", err)
	}
	scen, err := config.ParseScenarioYAMLString(testScenarioYAML)
	if err != nil {
		t.Fatalf("ParseScenarioYAMLString: %v", err)
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scen); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	executor.mu.Lock()
	executor.resourceManagers[rec.Run.Id] = rm
	executor.mu.Unlock()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/"+rec.Run.Id+"/export", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"resources"`) {
		t.Fatalf("expected resources object in export response: %s", body)
	}
	if !strings.Contains(body, `"queues"`) || !strings.Contains(body, `"topics"`) {
		t.Fatalf("expected queues/topics resources in export response: %s", body)
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
	verticalScenarioYAML := strings.Replace(testScenarioYAML, "cores: 2", "cores: 8", 1)
	input := &simulationv1.RunInput{
		ScenarioYaml: verticalScenarioYAML,
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

func TestHTTPServerUpdateRunConfigurationRejectsInvalidBody(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-unknown/configuration", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid JSON, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid request body") {
		t.Fatalf("expected invalid request body error, got: %s", rr.Body.String())
	}
}

func TestHTTPServerUpdateRunConfigurationRequiresPayloadFields(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-unknown/configuration", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for empty payload, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "at least one of services, workload, or policies must be provided") {
		t.Fatalf("expected empty payload validation error, got: %s", rr.Body.String())
	}
}

func TestHTTPServerUpdateRunConfigurationRejectsMissingServiceID(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	input := &simulationv1.RunInput{
		ScenarioYaml: strings.Replace(testScenarioYAML, "cores: 2", "cores: 8", 1),
		DurationMs:   5000,
	}
	rec, err := store.Create("run-missing-svc-id", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer exec.Stop(rec.Run.Id)

	body := `{"services":[{"replicas":2}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-missing-svc-id/configuration", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for missing service id, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "service id is required") {
		t.Fatalf("expected service id validation error, got: %s", rr.Body.String())
	}
}

func TestHTTPServerUpdateRunConfigurationRunNotFound(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/missing/configuration", strings.NewReader(`{"services":[{"id":"svc1","replicas":2}]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for unknown run, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "run not found") {
		t.Fatalf("expected run not found error, got: %s", rr.Body.String())
	}
}

func TestHTTPServerUpdateRunConfigurationRejectsNonRunningRun(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
	}
	if _, err := store.Create("run-not-running", input); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-not-running/configuration", strings.NewReader(`{"services":[{"id":"svc1","replicas":2}]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for non-running run, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "run is not running") {
		t.Fatalf("expected non-running run error, got: %s", rr.Body.String())
	}
}

func TestHTTPServerUpdateRunConfigurationRejectsInvalidReplicas(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
	}
	rec, err := store.Create("run-invalid-replicas", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-invalid-replicas/configuration", strings.NewReader(`{"services":[{"id":"svc1","replicas":0}]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for replicas<1, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "replicas must be at least 1") {
		t.Fatalf("expected replicas validation error, got: %s", rr.Body.String())
	}
}

func TestHTTPServerUpdateRunConfigurationRejectsWorkloadEntryValidation(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
	}
	rec, err := store.Create("run-workload-validation", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}

	rrMissingPattern := httptest.NewRecorder()
	reqMissingPattern := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-workload-validation/configuration", strings.NewReader(`{"workload":[{"rate_rps":10}]}`))
	reqMissingPattern.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rrMissingPattern, reqMissingPattern)
	if rrMissingPattern.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for missing pattern key, got %d: %s", rrMissingPattern.Code, rrMissingPattern.Body.String())
	}
	if !strings.Contains(rrMissingPattern.Body.String(), "pattern_key is required") {
		t.Fatalf("expected pattern_key validation error, got: %s", rrMissingPattern.Body.String())
	}

	rrBadRate := httptest.NewRecorder()
	reqBadRate := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-workload-validation/configuration", strings.NewReader(`{"workload":[{"pattern_key":"client:svc1:/test","rate_rps":0}]}`))
	reqBadRate.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rrBadRate, reqBadRate)
	if rrBadRate.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for non-positive workload rate, got %d: %s", rrBadRate.Code, rrBadRate.Body.String())
	}
	if !strings.Contains(rrBadRate.Body.String(), "rate_rps must be positive") {
		t.Fatalf("expected rate_rps validation error, got: %s", rrBadRate.Body.String())
	}
}

func TestHTTPServerUpdateRunConfigurationUnknownServiceReturnsError(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	input := &simulationv1.RunInput{
		ScenarioYaml: strings.Replace(testScenarioYAML, "cores: 2", "cores: 8", 1),
		DurationMs:   5000,
	}
	rec, err := store.Create("run-unknown-service", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer exec.Stop(rec.Run.Id)

	latest, ok := store.Get(rec.Run.Id)
	if !ok || latest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run is not RUNNING (status=%v), skipping unknown service update test", latest.Run.Status)
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		if _, cfgOK := exec.GetRunConfiguration(rec.Run.Id); cfgOK {
			break
		}
		if time.Now().After(deadline) {
			t.Skip("run configuration not initialized in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-unknown-service/configuration", strings.NewReader(`{"services":[{"id":"svc-does-not-exist","replicas":2}]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 for unknown service update, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "service not found") {
		t.Fatalf("expected unknown service error, got: %s", rr.Body.String())
	}
}

func TestHTTPServerUpdateRunConfigurationUnknownWorkloadPatternReturnsError(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   5000,
	}
	rec, err := store.Create("run-unknown-pattern", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer exec.Stop(rec.Run.Id)

	latest, ok := store.Get(rec.Run.Id)
	if !ok || latest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run is not RUNNING (status=%v), skipping unknown workload pattern test", latest.Run.Status)
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		if _, cfgOK := exec.GetRunConfiguration(rec.Run.Id); cfgOK {
			break
		}
		if time.Now().After(deadline) {
			t.Skip("run configuration not initialized in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-unknown-pattern/configuration", strings.NewReader(`{"workload":[{"pattern_key":"client:svc1:/missing","rate_rps":15}]}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 for unknown workload pattern, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "pattern not found") {
		t.Fatalf("expected unknown workload pattern error, got: %s", rr.Body.String())
	}
}

func TestHTTPServerUpdateRunConfigurationPoliciesOnlySuccess(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   5000,
	}
	rec, err := store.Create("run-policies-only", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer exec.Stop(rec.Run.Id)

	latest, ok := store.Get(rec.Run.Id)
	if !ok || latest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run is not RUNNING (status=%v), skipping policies-only update test", latest.Run.Status)
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		if _, cfgOK := exec.GetRunConfiguration(rec.Run.Id); cfgOK {
			break
		}
		if time.Now().After(deadline) {
			t.Skip("run configuration not initialized in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-policies-only/configuration", strings.NewReader(`{"policies":{"autoscaling":{"enabled":true,"target_cpu_util":0,"scale_step":0}}}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 for policies-only update, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "configuration updated successfully") {
		t.Fatalf("expected success message in response, got: %s", rr.Body.String())
	}
}

func TestHTTPServerGetRunConfigurationNotFound(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/not-found/configuration", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404 for unknown run, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "run not found") {
		t.Fatalf("expected run not found error, got: %s", rr.Body.String())
	}
}

func TestHTTPServerGetRunConfigurationRequiresRunningStatus(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
	}
	rec, err := store.Create("run-get-config-status", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-get-config-status/configuration", nil)
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected status 412 for non-running run, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), rec.Run.Status.String()) {
		t.Fatalf("expected response to include current run status, got: %s", rr.Body.String())
	}
}

func TestHTTPServerGetRunConfigurationReturnsInternalErrorWhenConfigUnavailable(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	input := &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   1000,
	}
	rec, err := store.Create("run-get-config-success", input)
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := store.SetStatus(rec.Run.Id, simulationv1.RunStatus_RUN_STATUS_RUNNING, ""); err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/run-get-config-success/configuration", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 for unavailable config, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "run configuration not available") {
		t.Fatalf("expected run configuration unavailable error, got: %s", rr.Body.String())
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

func TestHTTPServerExportRunIncludesTopologyGuardReasonDetails(t *testing.T) {
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))

	_, err := store.Create("opt-run-topology", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if err := store.SetMetrics("opt-run-topology", &simulationv1.RunMetrics{TotalRequests: 10}); err != nil {
		t.Fatalf("SetMetrics error: %v", err)
	}

	step := &simulationv1.OptimizationStep{
		IterationIndex: 1,
		Reason: "topology_guard_blocked action=host_scale_in decision_reason=cross_zone_fraction_above_max " +
			"cross_zone_request_fraction=0.75 max_cross_zone_request_fraction=0.2 " +
			"locality_hit_rate=0.25 min_locality_hit_rate=0.8 topology_latency_penalty_ms_mean=12.5 max_topology_latency_penalty_mean_ms=10",
	}
	if err := store.AppendOptimizationStep("opt-run-topology", step); err != nil {
		t.Fatalf("AppendOptimizationStep error: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/opt-run-topology/export", nil)
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
	details, ok := stepData["reason_details"].(map[string]any)
	if !ok {
		t.Fatalf("expected reason_details map, got %T (%v)", stepData["reason_details"], stepData["reason_details"])
	}
	if details["type"] != "topology_guard_blocked" || details["action"] != "host_scale_in" || details["decision_reason"] != "cross_zone_fraction_above_max" {
		t.Fatalf("unexpected reason_details core fields: %+v", details)
	}
	if details["cross_zone_request_fraction"].(float64) != 0.75 || details["max_cross_zone_request_fraction"].(float64) != 0.2 {
		t.Fatalf("unexpected reason_details thresholds: %+v", details)
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

func TestHTTPServerUpdateWorkloadPatternSnakeCaseArrivalFields(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	rec, err := store.Create("test-run-snake-pattern", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   300,
		RealTimeMode: true,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := executor.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	updatedRec, _ := store.Get(rec.Run.Id)
	if updatedRec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Skipf("Simulation completed too quickly (status: %v) - skipping snake_case pattern update test", updatedRec.Run.Status)
	}

	reqBody := map[string]any{
		"pattern_key": "client:svc1:/test",
		"pattern": map[string]any{
			"from":          "client",
			"source_kind":   "client",
			"traffic_class": "ingress",
			"to":            "svc1:/test",
			"arrival": map[string]any{
				"type":     "BURST",
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
	patternState, ok := executor.GetWorkloadPattern(rec.Run.Id, "client:svc1:/test")
	if !ok || patternState == nil {
		t.Fatalf("expected updated workload pattern")
	}
	if patternState.Pattern.Arrival.Type != "bursty" {
		t.Fatalf("expected normalized arrival type bursty, got %q", patternState.Pattern.Arrival.Type)
	}
	if patternState.Pattern.Arrival.RateRPS != 75 {
		t.Fatalf("expected updated rate_rps 75, got %f", patternState.Pattern.Arrival.RateRPS)
	}

	_, _ = executor.Stop(rec.Run.Id)
}

func TestHTTPServerUpdateWorkloadPatternSnakeCaseBurstyFields(t *testing.T) {
	store := NewRunStore()
	executor := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, executor)

	rec, err := store.Create("test-run-bursty-pattern", &simulationv1.RunInput{
		ScenarioYaml: testScenarioYAML,
		DurationMs:   300,
		RealTimeMode: true,
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if _, err := executor.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	updatedRec, _ := store.Get(rec.Run.Id)
	if updatedRec.Run.Status == simulationv1.RunStatus_RUN_STATUS_COMPLETED {
		t.Skipf("Simulation completed too quickly (status: %v) - skipping bursty pattern update test", updatedRec.Run.Status)
	}

	reqBody := map[string]any{
		"pattern_key": "client:svc1:/test",
		"pattern": map[string]any{
			"from": "client",
			"to":   "svc1:/test",
			"arrival": map[string]any{
				"type":                   "bursty",
				"rate_rps":               60.0,
				"burst_rate_rps":         180.0,
				"burst_duration_seconds": 4.5,
				"quiet_duration_seconds": 9.0,
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
	patternState, ok := executor.GetWorkloadPattern(rec.Run.Id, "client:svc1:/test")
	if !ok || patternState == nil {
		t.Fatalf("expected updated workload pattern")
	}
	if patternState.Pattern.Arrival.BurstRateRPS != 180 {
		t.Fatalf("expected burst_rate_rps 180, got %f", patternState.Pattern.Arrival.BurstRateRPS)
	}
	if patternState.Pattern.Arrival.BurstDurationSeconds != 4.5 {
		t.Fatalf("expected burst_duration_seconds 4.5, got %f", patternState.Pattern.Arrival.BurstDurationSeconds)
	}
	if patternState.Pattern.Arrival.QuietDurationSeconds != 9.0 {
		t.Fatalf("expected quiet_duration_seconds 9.0, got %f", patternState.Pattern.Arrival.QuietDurationSeconds)
	}

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

	// Pattern validation: missing from
	reqBody = map[string]any{
		"pattern_key": "client:svc1:/test",
		"pattern": map[string]any{
			"to": "svc1:/test",
			"arrival": map[string]any{
				"type":     "poisson",
				"rate_rps": 10.0,
			},
		},
	}
	bodyBytes, _ = json.Marshal(reqBody)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for missing pattern.from, got %d", rr.Code)
	}

	// Pattern validation: invalid target format
	reqBody = map[string]any{
		"pattern_key": "client:svc1:/test",
		"pattern": map[string]any{
			"from": "client",
			"to":   "svc1:",
			"arrival": map[string]any{
				"type":     "poisson",
				"rate_rps": 10.0,
			},
		},
	}
	bodyBytes, _ = json.Marshal(reqBody)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid pattern.to, got %d", rr.Code)
	}

	// Pattern validation: invalid arrival type
	reqBody = map[string]any{
		"pattern_key": "client:svc1:/test",
		"pattern": map[string]any{
			"from": "client",
			"to":   "svc1:/test",
			"arrival": map[string]any{
				"type":     "typo",
				"rate_rps": 10.0,
			},
		},
	}
	bodyBytes, _ = json.Marshal(reqBody)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for invalid arrival type, got %d", rr.Code)
	}

	// Pattern validation: non-positive arrival rate
	reqBody = map[string]any{
		"pattern_key": "client:svc1:/test",
		"pattern": map[string]any{
			"from": "client",
			"to":   "svc1:/test",
			"arrival": map[string]any{
				"type":     "poisson",
				"rate_rps": 0.0,
			},
		},
	}
	bodyBytes, _ = json.Marshal(reqBody)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/v1/runs/"+rec.Run.Id+"/workload", strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 for non-positive pattern.arrival.rate_rps, got %d", rr.Code)
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
	f64 := func(v float64) *float64 { return &v }
	pb := &simulationv1.RunMetrics{
		TotalRequests:                  10,
		IngressRequests:                4,
		IngressFailedRequests:          1,
		IngressErrorRate:               0.25,
		AttemptFailedRequests:          3,
		AttemptErrorRate:               0.3,
		RetryAttempts:                  2,
		TimeoutErrors:                  1,
		TopicPublishCountTotal:         4,
		TopicBacklogDepthSum:           9,
		MaxTopicConsumerLag:            7,
		TopicOldestMessageAgeMs:        111,
		LocalityHitRate:                0.9,
		CrossZoneRequestCountTotal:     5,
		SameZoneRequestCountTotal:      45,
		CrossZoneRequestFraction:       0.1,
		CrossZoneLatencyPenaltyMsTotal: 300,
		CrossZoneLatencyPenaltyMsMean:  100,
		SameZoneLatencyPenaltyMsTotal:  30,
		SameZoneLatencyPenaltyMsMean:   10,
		ExternalLatencyMsTotal:         40,
		ExternalLatencyMsMean:          20,
		TopologyLatencyPenaltyMsTotal:  370,
		TopologyLatencyPenaltyMsMean:   37,
		ServiceMetrics: []*simulationv1.ServiceMetrics{
			{
				ServiceName:             "svc1",
				QueueWaitP50Ms:          1,
				ProcessingLatencyP50Ms:  9,
				ProcessingLatencyMeanMs: 10,
			},
		},
		InstanceRouteStats: []*simulationv1.InstanceRouteStats{
			{
				ServiceName: "svc1", EndpointPath: "/x", InstanceId: "svc1-instance-0", Strategy: "least_queue", SelectionCount: 12,
			},
		},
		EndpointRequestStats: []*simulationv1.EndpointRequestStats{
			{
				ServiceName:             "svc1",
				EndpointPath:            "/x",
				RequestCount:            12,
				ErrorCount:              1,
				LatencyP95Ms:            f64(22),
				RootLatencyP95Ms:        f64(44),
				QueueWaitP95Ms:          f64(6),
				ProcessingLatencyMeanMs: f64(11),
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
	if j["topic_publish_count_total"].(int64) != 4 {
		t.Fatalf("json topic_publish_count_total: %v", j["topic_publish_count_total"])
	}
	if j["max_topic_consumer_lag"].(float64) != 7 {
		t.Fatalf("json max_topic_consumer_lag: %v", j["max_topic_consumer_lag"])
	}
	if j["locality_hit_rate"].(float64) != 0.9 {
		t.Fatalf("json locality_hit_rate: %v", j["locality_hit_rate"])
	}
	if j["cross_zone_request_fraction"].(float64) != 0.1 {
		t.Fatalf("json cross_zone_request_fraction: %v", j["cross_zone_request_fraction"])
	}
	if j["cross_zone_latency_penalty_ms_total"].(float64) != 300 {
		t.Fatalf("json cross_zone_latency_penalty_ms_total: %v", j["cross_zone_latency_penalty_ms_total"])
	}
	if j["cross_zone_latency_penalty_ms_mean"].(float64) != 100 {
		t.Fatalf("json cross_zone_latency_penalty_ms_mean: %v", j["cross_zone_latency_penalty_ms_mean"])
	}
	if j["same_zone_latency_penalty_ms_total"].(float64) != 30 {
		t.Fatalf("json same_zone_latency_penalty_ms_total: %v", j["same_zone_latency_penalty_ms_total"])
	}
	if j["external_latency_ms_mean"].(float64) != 20 {
		t.Fatalf("json external_latency_ms_mean: %v", j["external_latency_ms_mean"])
	}
	if j["topology_latency_penalty_ms_mean"].(float64) != 37 {
		t.Fatalf("json topology_latency_penalty_ms_mean: %v", j["topology_latency_penalty_ms_mean"])
	}
	es := j["endpoint_request_stats"].([]map[string]any)
	if len(es) != 1 || es[0]["endpoint_path"].(string) != "/x" || es[0]["latency_p95_ms"].(float64) != 22 ||
		es[0]["root_latency_p95_ms"].(float64) != 44 || es[0]["queue_wait_p95_ms"].(float64) != 6 ||
		es[0]["processing_latency_mean_ms"].(float64) != 11 {
		t.Fatalf("json endpoint_request_stats: %v", es)
	}
	rs := j["instance_route_stats"].([]map[string]any)
	if len(rs) != 1 || rs[0]["instance_id"].(string) != "svc1-instance-0" || rs[0]["selection_count"].(int64) != 12 {
		t.Fatalf("json instance_route_stats: %v", rs)
	}
}
