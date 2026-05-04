package simd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
	"github.com/GoSim-25-26J-441/simulation-core/internal/metrics"
	"github.com/GoSim-25-26J-441/simulation-core/internal/resource"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/config"
	"github.com/GoSim-25-26J-441/simulation-core/pkg/models"
)

func TestCPUPrimaryOptimizationHistoryReplayFields(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	runID := "cpu-hist-replay"
	if _, err := store.Create(runID, &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	scenario := &config.Scenario{
		Hosts: []config.Host{{ID: "h1", Cores: 1}},
		Services: []config.Service{
			{
				ID: "svc1", Replicas: 1, Model: "cpu", CPUCores: 1, MemoryMB: 128,
				Endpoints: []config.Endpoint{
					{Path: "/t", MeanCPUMs: 1, CPUSigmaMs: 0, NetLatencyMs: config.LatencySpec{Mean: 1, Sigma: 0}},
				},
			},
		},
		Workload: []config.WorkloadPattern{
			{From: "client", To: "svc1:/t", Arrival: config.ArrivalSpec{Type: "poisson", RateRPS: 1}},
		},
	}
	rm := resource.NewManager()
	if err := rm.InitializeFromScenario(scenario); err != nil {
		t.Fatalf("InitializeFromScenario: %v", err)
	}
	collector := metrics.NewCollector()
	collector.Start()
	state := mustScenarioState(t, scenario, rm, collector)
	wsStub, err := newWorkloadStateWithPatternsStub(runID, scenario, time.Now())
	if err != nil {
		t.Fatalf("newWorkloadStateWithPatternsStub: %v", err)
	}
	exec.mu.Lock()
	exec.resourceManagers[runID] = rm
	exec.workloadStates[runID] = wsStub
	exec.mu.Unlock()

	opt := &simulationv1.OptimizationConfig{
		Online:                    true,
		StepSize:                  1,
		MaxHosts:                  4,
		MinHosts:                  1,
		TargetUtilHigh:            0.7,
		TargetUtilLow:             0.4,
		TargetP95LatencyMs:        100,
		OptimizationTargetPrimary: "cpu_utilization",
	}
	runMetrics := &models.RunMetrics{
		LatencyP95:     5,
		TotalRequests:  100,
		FailedRequests: 2,
		ServiceMetrics: map[string]*models.ServiceMetrics{
			"svc1": {CPUUtilization: 0.75},
		},
	}
	tick := testOnlineTickInput(t, scenario, rm, runMetrics, opt)
	loop := &onlineCtrlLoopState{
		stableRepDown:     make(map[string]int),
		stableVertCPUDown: make(map[string]int),
		stableVertMemDown: make(map[string]int),
		prevErrFrac:       -1,
	}
	scratch := &cpuPrimaryScaleUpScratch{}
	exec.runOnlineControllerPolicyStep(runID, scenario, opt, rm, state, loop, tick, "cpu_utilization", true, scratch)

	rec, ok := store.Get(runID)
	if !ok {
		t.Fatal("expected run record")
	}
	if len(rec.OptimizationHistory) < 1 {
		t.Fatalf("expected at least one optimization step, got %d", len(rec.OptimizationHistory))
	}
	step := rec.OptimizationHistory[0]
	if step.GetPrimaryTarget() != "cpu_utilization" {
		t.Fatalf("primary_target: got %q", step.GetPrimaryTarget())
	}
	if step.GetObjectiveUnit() != "ratio" {
		t.Fatalf("objective_unit: got %q", step.GetObjectiveUnit())
	}
	if step.GetTargetUtilLow() != 0.4 || step.GetTargetUtilHigh() != 0.7 {
		t.Fatalf("target util band: low=%v high=%v", step.GetTargetUtilLow(), step.GetTargetUtilHigh())
	}
	if step.GetGuardrailP95Ms() != 100 || step.GetCurrentP95Ms() != 5 {
		t.Fatalf("p95 guardrail/current: guard=%v current=%v", step.GetGuardrailP95Ms(), step.GetCurrentP95Ms())
	}
	if step.GetTargetP95Ms() != 100 || step.GetScoreP95Ms() != 5 {
		t.Fatalf("legacy p95 fields: target=%v score=%v", step.GetTargetP95Ms(), step.GetScoreP95Ms())
	}
	if step.GetObjectiveScore() <= 0 || step.GetObjectiveScore() > 1 {
		t.Fatalf("objective_score out of range: %v", step.GetObjectiveScore())
	}
	if step.CurrentErrorRate == nil || step.GetCurrentErrorRate() != 0.02 {
		t.Fatalf("current_error_rate: %#v", step.CurrentErrorRate)
	}
}

func TestOptimizationHistoryExportIncludesReplayFields(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	if _, err := store.Create("replay-export", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetMetrics("replay-export", &simulationv1.RunMetrics{TotalRequests: 10}); err != nil {
		t.Fatalf("SetMetrics: %v", err)
	}
	er := 0.01
	step := &simulationv1.OptimizationStep{
		IterationIndex:      1,
		TargetP95Ms:         200,
		ScoreP95Ms:          48,
		Reason:              "service CPU above target, scaled replicas up",
		PrimaryTarget:       "cpu_utilization",
		ObjectiveScore:      0.83,
		ObjectiveUnit:       "ratio",
		TargetUtilLow:       0.4,
		TargetUtilHigh:      0.7,
		GuardrailP95Ms:      200,
		CurrentP95Ms:        48,
		CurrentErrorRate:    &er,
		DecisionMetric:      "service_cpu_utilization",
		DecisionMetricValue: 0.83,
		DecisionServiceId:   "service-1",
		Action:              "service_scale_out",
		PreviousConfig: &simulationv1.RunConfiguration{
			Services: []*simulationv1.ServiceConfigEntry{{ServiceId: "svc1", Replicas: 2}},
		},
		CurrentConfig: &simulationv1.RunConfiguration{
			Services: []*simulationv1.ServiceConfigEntry{{ServiceId: "svc1", Replicas: 3}},
		},
	}
	if err := store.AppendOptimizationStep("replay-export", step); err != nil {
		t.Fatalf("AppendOptimizationStep: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/replay-export/export", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("export status %d: %s", rr.Code, rr.Body.String())
	}
	var export map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &export); err != nil {
		t.Fatalf("json: %v", err)
	}
	runData := export["run"].(map[string]any)
	history := runData["optimization_history"].([]any)
	stepData := history[0].(map[string]any)
	if stepData["primary_target"] != "cpu_utilization" {
		t.Fatalf("primary_target: %v", stepData["primary_target"])
	}
	if stepData["objective_unit"] != "ratio" || stepData["objective_score"].(float64) != 0.83 {
		t.Fatalf("objective: %+v", stepData)
	}
	if stepData["target_util_low"].(float64) != 0.4 || stepData["target_util_high"].(float64) != 0.7 {
		t.Fatalf("util band: %+v", stepData)
	}
	if stepData["guardrail_p95_ms"].(float64) != 200 || stepData["current_p95_ms"].(float64) != 48 {
		t.Fatalf("p95 columns: %+v", stepData)
	}
	if stepData["target_p95_ms"].(float64) != 200 || stepData["score_p95_ms"].(float64) != 48 {
		t.Fatalf("legacy p95: %+v", stepData)
	}
	if stepData["decision_service_id"] != "service-1" || stepData["action"] != "service_scale_out" {
		t.Fatalf("decision ids: %+v", stepData)
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/v1/runs/replay-export", nil)
	srv.Handler().ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("get run status %d: %s", rr2.Code, rr2.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(rr2.Body.Bytes(), &detail); err != nil {
		t.Fatalf("json detail: %v", err)
	}
	runObj, ok := detail["run"].(map[string]any)
	if !ok {
		t.Fatalf("expected run object in GET /v1/runs response, got %T", detail["run"])
	}
	h2, ok := runObj["optimization_history"].([]any)
	if !ok || len(h2) != 1 {
		t.Fatalf("expected optimization_history in run detail, got %T %v", runObj["optimization_history"], runObj["optimization_history"])
	}
	s2 := h2[0].(map[string]any)
	if s2["primary_target"] != stepData["primary_target"] || s2["objective_score"] != stepData["objective_score"] {
		t.Fatalf("export vs detail mismatch: export=%+v detail=%+v", stepData["primary_target"], s2["primary_target"])
	}
}

func TestP95PrimaryOptimizationHistoryReplayBackwardCompatible(t *testing.T) {
	t.Parallel()
	store := NewRunStore()
	srv := NewHTTPServer(store, NewRunExecutor(store, nil))
	if _, err := store.Create("p95-replay", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 100}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SetMetrics("p95-replay", &simulationv1.RunMetrics{TotalRequests: 10}); err != nil {
		t.Fatalf("SetMetrics: %v", err)
	}
	step := &simulationv1.OptimizationStep{
		IterationIndex:      1,
		TargetP95Ms:         100,
		ScoreP95Ms:          95,
		Reason:              "p95 above target, scaled replicas up",
		PrimaryTarget:       "p95_latency",
		ObjectiveScore:      95,
		ObjectiveUnit:       "ms",
		GuardrailP95Ms:      100,
		CurrentP95Ms:        95,
		DecisionMetric:      "latency_p95_ms",
		DecisionMetricValue: 95,
		Action:              "service_scale_out",
		PreviousConfig: &simulationv1.RunConfiguration{
			Services: []*simulationv1.ServiceConfigEntry{{ServiceId: "svc1", Replicas: 2}},
		},
		CurrentConfig: &simulationv1.RunConfiguration{
			Services: []*simulationv1.ServiceConfigEntry{{ServiceId: "svc1", Replicas: 3}},
		},
	}
	if err := store.AppendOptimizationStep("p95-replay", step); err != nil {
		t.Fatalf("AppendOptimizationStep: %v", err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/runs/p95-replay/export", nil)
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var export map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &export); err != nil {
		t.Fatalf("json: %v", err)
	}
	runData := export["run"].(map[string]any)
	history := runData["optimization_history"].([]any)
	stepData := history[0].(map[string]any)
	if stepData["primary_target"] != "p95_latency" {
		t.Fatalf("primary_target: %v", stepData["primary_target"])
	}
	if stepData["target_p95_ms"].(float64) != 100 || stepData["score_p95_ms"].(float64) != 95 {
		t.Fatalf("legacy fields: %+v", stepData)
	}
	if stepData["objective_unit"] != "ms" || stepData["objective_score"].(float64) != 95 {
		t.Fatalf("objective: %+v", stepData)
	}
}
