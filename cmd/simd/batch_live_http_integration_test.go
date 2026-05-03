// Package main includes live HTTP integration tests for batch optimization (same binary as simd).
//
// Tests prefixed with TestLiveBatchOptimizationHTTP are skipped unless SIMD_BATCH_HTTP_LIVE=1.
// That gate keeps default CI and go test ./... fast: these paths spawn the full in-process HTTP
// server with the same optimizationRunnerAdapter wiring as cmd/simd/main.go and run real batch
// beam search plus simulations (minutes of CPU time when enabled).
//
// Do not remove the gate or run these tests unconditionally in CI without an explicit slower job.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
)

func requireSIMDBatchHTTPLive(t *testing.T) {
	t.Helper()
	if os.Getenv("SIMD_BATCH_HTTP_LIVE") != "1" {
		t.Skip("set SIMD_BATCH_HTTP_LIVE=1 for live HTTP batch tests (~minutes); skipped by default so CI stays fast")
	}
}

func liveBatchOptimizationHTTPServer(t *testing.T) (*simd.RunStore, http.Handler) {
	t.Helper()
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store, nil)
	executor.SetOptimizationRunner(&optimizationRunnerAdapter{store: store, executor: executor})
	srv := simd.NewHTTPServer(store, executor)
	return store, srv.Handler()
}

// Live scenario: frontend-shaped auth/user/db workload with three hosts (min_hosts=1 allows HOST_SCALE_IN exploration).
const batchOptimizationLiveScenarioYAML = `
hosts:
  - id: host-1
    cores: 8
    memory_gb: 32
  - id: host-2
    cores: 8
    memory_gb: 32
  - id: host-3
    cores: 8
    memory_gb: 32
services:
  - id: auth
    replicas: 2
    model: cpu
    cpu_cores: 2
    memory_mb: 1024
    endpoints:
      - path: /auth/login
        mean_cpu_ms: 50
        cpu_sigma_ms: 20
        default_memory_mb: 15
        downstream: []
        net_latency_ms:
          mean: 2
          sigma: 1
      - path: /auth/verify
        mean_cpu_ms: 5
        cpu_sigma_ms: 3
        default_memory_mb: 5
        downstream: []
        net_latency_ms:
          mean: 1
          sigma: 0.5
  - id: user
    replicas: 2
    model: mixed
    endpoints:
      - path: /user/get
        mean_cpu_ms: 12
        cpu_sigma_ms: 6
        downstream:
          - to: db:/db/query
            call_count_mean: 1
            call_latency_ms:
              mean: 10
              sigma: 5
            downstream_fraction_cpu: 0.6
        net_latency_ms:
          mean: 3
          sigma: 1
      - path: /user/update
        mean_cpu_ms: 25
        cpu_sigma_ms: 12
        downstream:
          - to: db:/db/query
            call_count_mean: 1
            call_latency_ms:
              mean: 15
              sigma: 8
            downstream_fraction_cpu: 0.7
        net_latency_ms:
          mean: 3
          sigma: 1
  - id: db
    replicas: 1
    model: db_latency
    endpoints:
      - path: /db/query
        mean_cpu_ms: 5
        cpu_sigma_ms: 2
        downstream: []
        net_latency_ms:
          mean: 1
          sigma: 0.5
workload:
  - from: client
    to: auth:/auth/login
    arrival:
      type: poisson
      rate_rps: 22
  - from: client
    to: user:/user/get
    arrival:
      type: poisson
      rate_rps: 5
`

// batchOptimizationPlacementStressYAML packs four replicas requiring ~5 CPUs each onto four 8-core hosts:
// at most one replica fits per host, so aggregate capacity stays feasible after HOST_SCALE_IN but the
// scheduler cannot place four replicas on three hosts — PlacementCapacityOK rejects those neighbors.
// SERVICE_SCALE_OUT to five replicas exceeds placability on four hosts for the same reason.
// SERVICE_SCALE_IN / vertical host tweaks remain feasible so beam search evaluates multiple candidates.
const batchOptimizationPlacementStressYAML = `
hosts:
  - id: host-1
    cores: 8
    memory_gb: 32
  - id: host-2
    cores: 8
    memory_gb: 32
  - id: host-3
    cores: 8
    memory_gb: 32
  - id: host-4
    cores: 8
    memory_gb: 32
services:
  - id: api
    replicas: 4
    model: cpu
    cpu_cores: 5
    memory_mb: 512
    endpoints:
      - path: /ping
        mean_cpu_ms: 12
        cpu_sigma_ms: 6
        default_memory_mb: 12
        downstream: []
        net_latency_ms:
          mean: 1
          sigma: 0.5
workload:
  - from: client
    to: api:/ping
    arrival:
      type: poisson
      rate_rps: 15
`

// TestLiveBatchOptimizationHTTPUsesProductionRunner wires the same optimizationRunnerAdapter as main()
// and drives POST /v1/runs → POST start → GET poll → GET export like the frontend/backend.
//
//	go test ./cmd/simd -run TestLiveBatchOptimizationHTTPUsesProductionRunner -timeout 15m -v
//	SIMD_BATCH_HTTP_LIVE=1 ...
func TestLiveBatchOptimizationHTTPUsesProductionRunner(t *testing.T) {
	requireSIMDBatchHTTPLive(t)
	_, h := liveBatchOptimizationHTTPServer(t)

	allowed := []interface{}{
		float64(1), float64(2), float64(3), float64(4), float64(5), float64(6),
		float64(7), float64(8), float64(9), float64(10), float64(11), float64(12),
	}
	payload := map[string]any{
		"input": map[string]any{
			"scenario_yaml": strings.TrimSpace(batchOptimizationLiveScenarioYAML),
			"duration_ms":   float64(400),
			"optimization": map[string]any{
				"objective":       "cpu_utilization",
				"max_evaluations": float64(128),
				"batch": map[string]any{
					"allowed_actions":                 allowed,
					"max_neighbors_per_state":         float64(24),
					"max_search_depth":                float64(5),
					"beam_width":                      float64(3),
					"infeasible_beam_width":           float64(1),
					"host_cpu_utilization_band":       map[string]any{"low": 0.45, "high": 0.75},
					"host_memory_utilization_band":    map[string]any{"low": 0.35, "high": 0.8},
					"service_cpu_utilization_band":    map[string]any{"low": 0.5, "high": 0.7},
					"service_memory_utilization_band": map[string]any{"low": 0.4, "high": 0.75},
					"min_hosts":                       float64(1),
					"max_hosts":                       float64(4),
					"enable_local_refinement":         false,
					"freeze_workload":                 true,
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	run := created["run"].(map[string]any)
	parentID := run["id"].(string)

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/runs/"+parentID, nil)
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", rr2.Code, rr2.Body.String())
	}

	deadline := time.Now().Add(12 * time.Minute)
	var final map[string]any
	for time.Now().Before(deadline) {
		rrg := httptest.NewRecorder()
		reqg := httptest.NewRequest(http.MethodGet, "/v1/runs/"+parentID, nil)
		h.ServeHTTP(rrg, reqg)
		if rrg.Code != http.StatusOK {
			t.Fatalf("get status=%d", rrg.Code)
		}
		var resp map[string]any
		if err := json.Unmarshal(rrg.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		final = resp["run"].(map[string]any)
		st := final["status"].(string)
		if st == "RUN_STATUS_COMPLETED" || st == "RUN_STATUS_FAILED" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	t.Logf("batch_live_verification parent_run_id=%s status=%v error=%v best_run_id=%v iterations=%v summary=%v feasible=%v violation=%v efficiency=%v",
		parentID, final["status"], final["error"], final["best_run_id"], final["iterations"],
		final["batch_recommendation_summary"], final["batch_recommendation_feasible"],
		final["batch_violation_score"], final["batch_efficiency_score"])

	if final["status"].(string) != "RUN_STATUS_COMPLETED" {
		t.Fatalf("expected parent completed, got status=%v err=%v", final["status"], final["error"])
	}
	errStr, _ := final["error"].(string)
	for _, needle := range []string{"resource initialization failed", "cannot place service", "insufficient host capacity"} {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(needle)) {
			t.Fatalf("parent error must not indicate placement failure: %q", errStr)
		}
	}
	bestID, _ := final["best_run_id"].(string)
	if bestID == "" {
		t.Fatal("expected best_run_id")
	}
	if _, ok := final["batch_recommendation_feasible"]; !ok {
		t.Fatal("expected batch_recommendation_feasible")
	}
	diag, ok := final["batch_search_diagnostics"].(map[string]any)
	if !ok {
		t.Fatal("expected batch_search_diagnostics map on parent run JSON")
	}
	for _, key := range []string{"generated_neighbors", "rejected_static_capacity", "rejected_bounds", "rejected_placement", "evaluated_candidates", "failed_candidate_evaluations"} {
		v, ok := diag[key].(float64)
		if !ok {
			t.Fatalf("diag %s missing or wrong type %T", key, diag[key])
		}
		if v < 0 {
			t.Fatalf("diag %s negative %v", key, v)
		}
	}
	t.Logf("batch_search_diagnostics=%v", diag)

	rre := httptest.NewRecorder()
	reqe := httptest.NewRequest(http.MethodGet, "/v1/runs/"+bestID+"/export", nil)
	h.ServeHTTP(rre, reqe)
	if rre.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", rre.Code, rre.Body.String())
	}
	var export map[string]any
	if err := json.Unmarshal(rre.Body.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	fcfg, ok := export["final_config"].(map[string]any)
	if !ok {
		t.Fatal("expected export.final_config on best candidate")
	}
	hosts, ok := fcfg["hosts"].([]interface{})
	if !ok || len(hosts) == 0 {
		t.Fatalf("expected export.final_config.hosts nonempty, got %#v", fcfg["hosts"])
	}
	t.Logf("best_candidate_export hosts_count=%d", len(hosts))
}

// TestLiveBatchOptimizationHTTPNonBaselinePlacementRejections exercises beam neighbors that survive
// structural bounds but fail placement preflight (HOST_SCALE_IN / SERVICE_SCALE_OUT) alongside neighbors
// that simulate successfully (SERVICE_SCALE_IN, host vertical tweaks). Requires SIMD_BATCH_HTTP_LIVE=1.
//
//	go test ./cmd/simd -run TestLiveBatchOptimizationHTTPNonBaselinePlacementRejections -timeout 15m -v
func TestLiveBatchOptimizationHTTPNonBaselinePlacementRejections(t *testing.T) {
	requireSIMDBatchHTTPLive(t)
	_, h := liveBatchOptimizationHTTPServer(t)

	allowed := []interface{}{
		float64(1), float64(2), float64(3), float64(4), float64(5), float64(6),
		float64(7), float64(8), float64(9), float64(10), float64(11), float64(12),
	}
	payload := map[string]any{
		"input": map[string]any{
			"scenario_yaml": strings.TrimSpace(batchOptimizationPlacementStressYAML),
			"duration_ms":   float64(400),
			"optimization": map[string]any{
				"objective":       "cpu_utilization",
				"max_evaluations": float64(96),
				"batch": map[string]any{
					"allowed_actions":                 allowed,
					"max_neighbors_per_state":         float64(28),
					"max_search_depth":                float64(4),
					"beam_width":                      float64(4),
					"infeasible_beam_width":           float64(2),
					"host_cpu_utilization_band":       map[string]any{"low": 0.05, "high": 0.98},
					"host_memory_utilization_band":    map[string]any{"low": 0.05, "high": 0.98},
					"service_cpu_utilization_band":    map[string]any{"low": 0.05, "high": 0.98},
					"service_memory_utilization_band": map[string]any{"low": 0.05, "high": 0.98},
					"min_hosts":                       float64(1),
					"max_hosts":                       float64(8),
					// Without explicit mins, baseline-derived MinHostCPU (=8) rejects HOST_SCALE_DOWN neighbors.
					"min_host_cpu_cores":              float64(2),
					"max_host_cpu_cores":              float64(64),
					"min_host_memory_gb":              float64(8),
					"max_host_memory_gb":              float64(256),
					"min_cpu_cores_per_instance":      float64(0.25),
					"max_cpu_cores_per_instance":      float64(32),
					"min_memory_mb_per_instance":      float64(128),
					"max_memory_mb_per_instance":      float64(65536),
					"enable_local_refinement":         false,
					"freeze_workload":                 true,
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	run := created["run"].(map[string]any)
	parentID := run["id"].(string)

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/runs/"+parentID, nil)
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", rr2.Code, rr2.Body.String())
	}

	deadline := time.Now().Add(12 * time.Minute)
	var final map[string]any
	for time.Now().Before(deadline) {
		rrg := httptest.NewRecorder()
		reqg := httptest.NewRequest(http.MethodGet, "/v1/runs/"+parentID, nil)
		h.ServeHTTP(rrg, reqg)
		if rrg.Code != http.StatusOK {
			t.Fatalf("get status=%d", rrg.Code)
		}
		var resp map[string]any
		if err := json.Unmarshal(rrg.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		final = resp["run"].(map[string]any)
		st := final["status"].(string)
		if st == "RUN_STATUS_COMPLETED" || st == "RUN_STATUS_FAILED" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	t.Logf("batch_live_placement_stress parent_run_id=%s status=%v error=%v best_run_id=%v iterations=%v summary=%v feasible=%v violation=%v efficiency=%v",
		parentID, final["status"], final["error"], final["best_run_id"], final["iterations"],
		final["batch_recommendation_summary"], final["batch_recommendation_feasible"],
		final["batch_violation_score"], final["batch_efficiency_score"])

	if final["status"].(string) != "RUN_STATUS_COMPLETED" {
		t.Fatalf("expected parent completed, got status=%v err=%v", final["status"], final["error"])
	}
	errStr, _ := final["error"].(string)
	for _, needle := range []string{"resource initialization failed", "cannot place service", "insufficient host capacity"} {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(needle)) {
			t.Fatalf("parent error must not indicate placement failure: %q", errStr)
		}
	}
	bestID, _ := final["best_run_id"].(string)
	if bestID == "" {
		t.Fatal("expected best_run_id")
	}
	diag, ok := final["batch_search_diagnostics"].(map[string]any)
	if !ok {
		t.Fatal("expected batch_search_diagnostics map on parent run JSON")
	}
	gen := diag["generated_neighbors"].(float64)
	ev := diag["evaluated_candidates"].(float64)
	rp := diag["rejected_placement"].(float64)
	failed := diag["failed_candidate_evaluations"].(float64)
	t.Logf("batch_search_diagnostics=%v", diag)
	if gen <= 0 {
		t.Fatalf("expected generated_neighbors > 0, got %v", gen)
	}
	if ev <= 1 {
		t.Fatalf("expected evaluated_candidates > 1 (non-baseline neighbors), got %v", ev)
	}
	if rp <= 0 && failed <= 0 {
		t.Fatalf("expected rejected_placement > 0 or failed_candidate_evaluations > 0, got rp=%v failed=%v", rp, failed)
	}

	rre := httptest.NewRecorder()
	reqe := httptest.NewRequest(http.MethodGet, "/v1/runs/"+bestID+"/export", nil)
	h.ServeHTTP(rre, reqe)
	if rre.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", rre.Code, rre.Body.String())
	}
	var export map[string]any
	if err := json.Unmarshal(rre.Body.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	runExport, ok := export["run"].(map[string]any)
	if !ok {
		t.Fatal("expected export.run")
	}
	if st := runExport["status"].(string); st != "RUN_STATUS_COMPLETED" {
		t.Fatalf("best candidate run status must be completed, got %q", st)
	}
	bestErr, _ := runExport["error"].(string)
	for _, needle := range []string{"resource initialization failed", "cannot place service", "insufficient host capacity"} {
		if strings.Contains(strings.ToLower(bestErr), strings.ToLower(needle)) {
			t.Fatalf("best export must not be a failed-placement child: error=%q", bestErr)
		}
	}
	fcfg, ok := export["final_config"].(map[string]any)
	if !ok {
		t.Fatal("expected export.final_config on best candidate")
	}
	hosts, ok := fcfg["hosts"].([]interface{})
	if !ok || len(hosts) == 0 {
		t.Fatalf("expected export.final_config.hosts nonempty, got %#v", fcfg["hosts"])
	}
	inputObj, ok := export["input"].(map[string]any)
	if !ok {
		t.Fatal("expected export.input")
	}
	scenarioYAML, _ := inputObj["scenario_yaml"].(string)
	if strings.TrimSpace(scenarioYAML) == "" {
		t.Fatal("expected nonempty export.input.scenario_yaml")
	}
	t.Logf("best_candidate_export hosts_count=%d", len(hosts))
}
