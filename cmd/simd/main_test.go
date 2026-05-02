package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/improvement"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
)

func TestBuildTopCandidateRunIDs_Deduplication(t *testing.T) {
	// Same RunID repeated in multiple history steps (e.g. same config won 4 times)
	r := &improvement.ExperimentResult{
		BestRunID: "opt-best",
		Runs: []*improvement.RunContext{
			{RunID: "opt-A", Score: 10},
			{RunID: "opt-best", Score: 5},
			{RunID: "opt-best", Score: 5},
			{RunID: "opt-best", Score: 5},
			{RunID: "opt-best", Score: 5},
		},
	}
	// n=5 and len(runs)==5: "return all" branch, unique IDs in first-occurrence order
	got := buildTopCandidateRunIDs(r, 5)
	want := []string{"opt-A", "opt-best"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildTopCandidateRunIDs(r, 5) = %v, want %v", got, want)
	}
	// n=0: return all unique, first-occurrence order gives opt-A then opt-best
	gotAll := buildTopCandidateRunIDs(r, 0)
	wantAll := []string{"opt-A", "opt-best"}
	if !reflect.DeepEqual(gotAll, wantAll) {
		t.Errorf("buildTopCandidateRunIDs(r, 0) = %v, want %v", gotAll, wantAll)
	}
}

func TestBuildTopCandidateRunIDs_TopNByScore(t *testing.T) {
	// n=3, multiple runs with same score (same ID repeated)
	r := &improvement.ExperimentResult{
		BestRunID: "opt-best",
		Runs: []*improvement.RunContext{
			{RunID: "opt-1", Score: 100},
			{RunID: "opt-2", Score: 50},
			{RunID: "opt-2", Score: 50},
			{RunID: "opt-3", Score: 25},
			{RunID: "opt-4", Score: 10},
		},
	}
	got := buildTopCandidateRunIDs(r, 3)
	// Top 3 by score (unique): opt-4 (10), opt-3 (25), opt-2 (50). Best run opt-best not in top 3 so appended
	want := []string{"opt-4", "opt-3", "opt-2", "opt-best"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildTopCandidateRunIDs(r, 3) = %v, want %v", got, want)
	}
}

func TestBuildTopCandidateRunIDs_BestInTopN(t *testing.T) {
	// Best run is already in top 3, should not be duplicated
	r := &improvement.ExperimentResult{
		BestRunID: "opt-best",
		Runs: []*improvement.RunContext{
			{RunID: "opt-1", Score: 100},
			{RunID: "opt-best", Score: 10},
			{RunID: "opt-2", Score: 50},
			{RunID: "opt-3", Score: 25},
		},
	}
	got := buildTopCandidateRunIDs(r, 3)
	want := []string{"opt-best", "opt-3", "opt-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildTopCandidateRunIDs(r, 3) = %v, want %v", got, want)
	}
}

func TestBuildTopCandidateRunIDs_NoDuplicatesInInput(t *testing.T) {
	// Existing behavior: 6 unique runs, n=5 -> 5 elements; best_run_id in first 5 so not appended again
	r := &improvement.ExperimentResult{
		BestRunID: "opt-cand-best",
		Runs: []*improvement.RunContext{
			{RunID: "opt-cand-best", Score: 5},
			{RunID: "opt-cand-2", Score: 10},
			{RunID: "opt-cand-3", Score: 15},
			{RunID: "opt-cand-4", Score: 20},
			{RunID: "opt-cand-5", Score: 25},
			{RunID: "opt-cand-6", Score: 30},
		},
	}
	got := buildTopCandidateRunIDs(r, 5)
	want := []string{"opt-cand-best", "opt-cand-2", "opt-cand-3", "opt-cand-4", "opt-cand-5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildTopCandidateRunIDs(r, 5) = %v, want %v", got, want)
	}
}

func TestBuildTopCandidateRunIDs_BestNotInFirstFive(t *testing.T) {
	// 6 unique runs, best is 6th by score; n=5 -> first 5 by score + best_run_id appended
	r := &improvement.ExperimentResult{
		BestRunID: "opt-worst-by-score",
		Runs: []*improvement.RunContext{
			{RunID: "opt-1", Score: 10},
			{RunID: "opt-2", Score: 20},
			{RunID: "opt-3", Score: 30},
			{RunID: "opt-4", Score: 40},
			{RunID: "opt-5", Score: 50},
			{RunID: "opt-worst-by-score", Score: 100},
		},
	}
	got := buildTopCandidateRunIDs(r, 5)
	want := []string{"opt-1", "opt-2", "opt-3", "opt-4", "opt-5", "opt-worst-by-score"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildTopCandidateRunIDs(r, 5) = %v, want %v", got, want)
	}
}

func TestBuildTopCandidateRunIDs_EmptyRuns(t *testing.T) {
	r := &improvement.ExperimentResult{BestRunID: "best", Runs: nil}
	got := buildTopCandidateRunIDs(r, 5)
	if len(got) != 0 {
		t.Errorf("buildTopCandidateRunIDs(nil runs, 5) = %v, want []", got)
	}
	got0 := buildTopCandidateRunIDs(r, 0)
	if len(got0) != 0 {
		t.Errorf("buildTopCandidateRunIDs(nil runs, 0) = %v, want []", got0)
	}
}

func TestGetCallbackWhitelist(t *testing.T) {
	t.Setenv(envCallbackWhitelist, "")
	if got := getCallbackWhitelist(); got != nil {
		t.Fatalf("expected nil for empty env, got %v", got)
	}

	t.Setenv(envCallbackWhitelist, "  ,  ")
	if got := getCallbackWhitelist(); got != nil {
		t.Fatalf("expected nil for whitespace env, got %v", got)
	}

	t.Setenv(envCallbackWhitelist, "example.com, 10.0.0.1 ,localhost")
	got := getCallbackWhitelist()
	want := []string{"example.com", "10.0.0.1", "localhost"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("getCallbackWhitelist() = %v, want %v", got, want)
	}
}

func TestGetTopCandidatesN(t *testing.T) {
	_ = os.Unsetenv(envTopCandidates)
	if got := getTopCandidatesN(); got != 0 {
		t.Fatalf("unset env expected 0, got %d", got)
	}

	t.Setenv(envTopCandidates, "-1")
	if got := getTopCandidatesN(); got != 0 {
		t.Fatalf("negative env expected 0, got %d", got)
	}

	t.Setenv(envTopCandidates, "not-a-number")
	if got := getTopCandidatesN(); got != 0 {
		t.Fatalf("invalid env expected 0, got %d", got)
	}

	t.Setenv(envTopCandidates, "7")
	if got := getTopCandidatesN(); got != 7 {
		t.Fatalf("valid env expected 7, got %d", got)
	}
}

func TestTrimTopBatchCandidates(t *testing.T) {
	ordered := []string{"a", "b", "a", "", "c", "d"}

	gotAll := trimTopBatchCandidates(ordered, "c", 0)
	wantAll := []string{"a", "b", "c", "d"}
	if !reflect.DeepEqual(gotAll, wantAll) {
		t.Fatalf("trimTopBatchCandidates(all) = %v, want %v", gotAll, wantAll)
	}

	gotTop := trimTopBatchCandidates(ordered, "d", 2)
	wantTop := []string{"a", "b", "d"}
	if !reflect.DeepEqual(gotTop, wantTop) {
		t.Fatalf("trimTopBatchCandidates(top) = %v, want %v", gotTop, wantTop)
	}

	gotNoDupBest := trimTopBatchCandidates([]string{"x", "y", "z"}, "y", 2)
	wantNoDupBest := []string{"x", "y"}
	if !reflect.DeepEqual(gotNoDupBest, wantNoDupBest) {
		t.Fatalf("trimTopBatchCandidates(no dup best) = %v, want %v", gotNoDupBest, wantNoDupBest)
	}
}

const gatewayLeastQueueBatchScenarioYAML = `
hosts:
  - id: host-1
    cores: 8
    memory_gb: 32
  - id: host-2
    cores: 8
    memory_gb: 32
services:
  - id: gateway-1
    kind: api_gateway
    role: ingress
    replicas: 2
    model: cpu
    cpu_cores: 1.4
    memory_mb: 768
    routing:
      strategy: least_queue
    scaling:
      horizontal: true
      vertical_cpu: true
      vertical_memory: true
    endpoints:
      - path: /ingress
        mean_cpu_ms: 5
        cpu_sigma_ms: 0.5
        default_memory_mb: 16
        downstream:
          - to: service-1:/read
            mode: sync
            kind: rest
            probability: 1
            call_count_mean: 1
            call_latency_ms: {mean: 4, sigma: 1}
          - to: service-2:/read
            mode: sync
            kind: rest
            probability: 1
            call_count_mean: 1
            call_latency_ms: {mean: 4, sigma: 1}
        net_latency_ms: {mean: 2, sigma: 0.5}
  - id: service-1
    kind: service
    replicas: 2
    model: cpu
    cpu_cores: 2
    memory_mb: 512
    endpoints:
      - path: /read
        mean_cpu_ms: 8
        cpu_sigma_ms: 2
        default_memory_mb: 20
        downstream: []
        net_latency_ms: {mean: 2, sigma: 0.5}
  - id: service-2
    kind: service
    replicas: 2
    model: cpu
    cpu_cores: 2
    memory_mb: 512
    endpoints:
      - path: /read
        mean_cpu_ms: 8
        cpu_sigma_ms: 2
        default_memory_mb: 20
        downstream: []
        net_latency_ms: {mean: 2, sigma: 0.5}
workload:
  - from: client
    to: gateway-1:/ingress
    arrival:
      type: constant
      rate_rps: 180
`

func TestHTTPBatchOptimizationGatewayLeastQueueReplay(t *testing.T) {
	store := simd.NewRunStore()
	defer store.Stop()
	executor := simd.NewRunExecutor(store, nil)
	executor.SetOptimizationRunner(&optimizationRunnerAdapter{store: store, executor: executor})
	srv := simd.NewHTTPServer(store, executor)

	createBody := map[string]any{
		"input": map[string]any{
			"scenario_yaml": gatewayLeastQueueBatchScenarioYAML,
			"duration_ms":   2000,
			"seed":          123,
			"optimization": map[string]any{
				"objective":       "p95_latency_ms",
				"max_evaluations": 4,
				"batch": map[string]any{
					"max_p95_latency_ms":          500,
					"max_p99_latency_ms":          1000,
					"max_error_rate":              0.05,
					"min_throughput_rps":          165,
					"beam_width":                  2,
					"max_search_depth":            1,
					"max_neighbors_per_state":     3,
					"reevaluations_per_candidate": 1,
					"infeasible_beam_width":       1,
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(createBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create run: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var createResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &createResp); err != nil {
		t.Fatal(err)
	}
	runID := createResp["run"].(map[string]any)["id"].(string)

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID, nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("start run: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var run map[string]any
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/v1/runs/"+runID, nil)
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("get run: expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var getResp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &getResp); err != nil {
			t.Fatal(err)
		}
		run = getResp["run"].(map[string]any)
		switch run["status"] {
		case "RUN_STATUS_COMPLETED":
			goto completed
		case "RUN_STATUS_FAILED":
			t.Fatalf("batch optimization failed: %v", run["error"])
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("batch optimization did not complete")

completed:
	if run["best_run_id"] == "" {
		t.Fatalf("expected best_run_id in completed optimization run: %#v", run)
	}
	bestRunID := run["best_run_id"].(string)
	if ids, ok := run["candidate_run_ids"].([]any); !ok || len(ids) == 0 {
		t.Fatalf("expected candidate_run_ids in completed optimization run: %#v", run)
	}
	if replay, ok := run["optimization_replay"].(map[string]any); !ok || replay["scenario_config_hash"] == "" {
		t.Fatalf("expected optimization_replay with scenario_config_hash, got %#v", run["optimization_replay"])
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs/"+runID+"/metrics", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get metrics: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var metricsResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &metricsResp); err != nil {
		t.Fatal(err)
	}
	metricsObj := metricsResp["metrics"].(map[string]any)
	routeRows := metricsObj["instance_route_stats"].([]any)
	var total, maxSel float64
	var gatewayRows int
	for _, raw := range routeRows {
		row := raw.(map[string]any)
		if row["service_name"] != "gateway-1" || row["endpoint_path"] != "/ingress" || row["strategy"] != "least_queue" {
			continue
		}
		gatewayRows++
		n := row["selection_count"].(float64)
		total += n
		if n > maxSel {
			maxSel = n
		}
	}
	if gatewayRows < 2 || total == 0 {
		t.Fatalf("expected route stats for both gateway replicas, rows=%v", routeRows)
	}
	if share := maxSel / total; share >= 0.95 {
		t.Fatalf("gateway least_queue routing still stuck on one replica: maxShare=%.3f rows=%v", share, routeRows)
	}
	if p95 := metricsObj["latency_p95_ms"].(float64); p95 > 30_000 {
		t.Fatalf("root p95 too high after HTTP batch replay: %.1f ms", p95)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/runs/"+bestRunID+"/export", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("export best candidate: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var exportResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &exportResp); err != nil {
		t.Fatal(err)
	}
	input, ok := exportResp["input"].(map[string]any)
	if !ok {
		t.Fatalf("expected retained best candidate input export, got %#v", exportResp)
	}
	if input["scenario_yaml"] == "" || input["duration_ms"].(float64) != 2000 {
		t.Fatalf("expected replayable best candidate input, got %#v", input)
	}
	if _, ok := exportResp["final_config"].(map[string]any); !ok {
		t.Fatalf("expected retained best candidate final_config export, got %#v", exportResp)
	}
}
