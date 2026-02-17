//go:build integration
// +build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/GoSim-25-26J-441/simulation-core/internal/improvement"
	"github.com/GoSim-25-26J-441/simulation-core/internal/simd"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
)

// testOptimizationAdapter implements simd.OptimizationRunner for integration tests
type testOptimizationAdapter struct {
	store    *simd.RunStore
	executor *simd.RunExecutor
}

func (a *testOptimizationAdapter) RunExperiment(ctx context.Context, scenario *config.Scenario, durationMs int64, params *simd.OptimizationParams) (string, float64, int32, error) {
	objective, err := improvement.NewObjectiveFunction(params.Objective)
	if err != nil {
		return "", 0, 0, err
	}
	maxIter := int(params.MaxIterations)
	if maxIter <= 0 {
		maxIter = 10
	}
	stepSize := params.StepSize
	if stepSize <= 0 {
		stepSize = 1.0
	}
	optimizer := improvement.NewOptimizer(objective, maxIter, stepSize)
	orchestrator := improvement.NewOrchestrator(a.store, a.executor, optimizer, objective)

	done := make(chan struct {
		bestRunID  string
		bestScore  float64
		iterations int32
		err        error
	}, 1)
	go func() {
		r, err := orchestrator.RunExperiment(ctx, scenario, durationMs)
		if err != nil {
			done <- struct {
				bestRunID  string
				bestScore  float64
				iterations int32
				err        error
			}{err: err}
			return
		}
		done <- struct {
			bestRunID  string
			bestScore  float64
			iterations int32
			err        error
		}{r.BestRunID, r.BestScore, int32(r.Iterations), nil}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			return "", 0, 0, res.err
		}
		return res.bestRunID, res.bestScore, res.iterations, nil
	case <-ctx.Done():
		orchestrator.CancelActiveRuns()
		<-done
		return "", 0, 0, ctx.Err()
	}
}

func TestIntegration_Optimization_CreateStartAndComplete(t *testing.T) {
	store := simd.NewRunStore()
	executor := simd.NewRunExecutor(store)
	executor.SetOptimizationRunner(&testOptimizationAdapter{store: store, executor: executor})
	srv := simd.NewHTTPServer(store, executor)

	// 1. Create optimization run via HTTP
	createBody := map[string]any{
		"input": map[string]any{
			"scenario_yaml": testScenarioYAML,
			"duration_ms":   2000,
			"optimization": map[string]any{
				"objective":      "p95_latency_ms",
				"max_iterations": 2,
				"step_size":      1.0,
			},
		},
	}
	bodyBytes, _ := json.Marshal(createBody)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/runs", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var createResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	runData, ok := createResp["run"].(map[string]any)
	if !ok {
		t.Fatalf("expected run object")
	}
	runID, ok := runData["id"].(string)
	if !ok || runID == "" {
		t.Fatalf("expected run id")
	}

	// 2. Start the run via HTTP (POST /v1/runs/{id})
	startBody := map[string]any{}
	startBytes, _ := json.Marshal(startBody)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/runs/"+runID, bytes.NewReader(startBytes))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 on start, got %d: %s", rr.Code, rr.Body.String())
	}

	// 3. Poll for completion (optimization with 2 iterations can take ~30-60s)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/v1/runs/"+runID, nil)
		srv.Handler().ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 on get, got %d", rr.Code)
		}

		var getResp map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &getResp); err != nil {
			t.Fatalf("invalid json: %v", err)
		}
		run, ok := getResp["run"].(map[string]any)
		if !ok {
			t.Fatalf("expected run object")
		}
		status, _ := run["status"].(string)

		switch status {
		case "RUN_STATUS_COMPLETED":
			bestRunID, _ := run["best_run_id"].(string)
			if bestRunID == "" {
				t.Fatalf("expected best_run_id for completed optimization run")
			}
			if _, ok := run["best_score"]; !ok {
				t.Fatalf("expected best_score for completed optimization run")
			}
			if _, ok := run["iterations"]; !ok {
				t.Fatalf("expected iterations for completed optimization run")
			}
			return
		case "RUN_STATUS_FAILED":
			t.Fatalf("optimization run failed: %v", run["error"])
		}

		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("optimization run did not complete within deadline")
}
