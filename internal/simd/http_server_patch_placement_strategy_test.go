package simd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	simulationv1 "github.com/GoSim-25-26J-441/simulation-core/gen/go/simulation/v1"
)

func TestHTTPServerPatchConfigurationStrictRejectsServiceCPUOverHostCapacity(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rec, err := store.Create("run-strict-cap", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 2000})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	recLatest, ok := store.Get(rec.Run.Id)
	if !ok || recLatest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run not RUNNING")
	}
	defer exec.Stop(rec.Run.Id)

	body := map[string]any{
		"services": []map[string]any{{
			"id": "svc1", "replicas": 1, "cpu_cores": 3.0,
		}},
	}
	b, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-strict-cap/configuration", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "host CPU capacity exceeded") {
		t.Fatalf("expected capacity error, got %s", rr.Body.String())
	}
}

func TestHTTPServerPatchConfigurationExpandThenAppliesCPUIncrease(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rec, err := store.Create("run-expand-cpu", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 2000})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	recLatest, ok := store.Get(rec.Run.Id)
	if !ok || recLatest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run not RUNNING")
	}
	defer exec.Stop(rec.Run.Id)

	body := map[string]any{
		"placement_strategy": httpPlacementStrategyExpandCapacityIfNeeded,
		"services": []map[string]any{{
			"id": "svc1", "replicas": 1, "cpu_cores": 3.0,
		}},
	}
	b, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-expand-cpu/configuration", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["placement_strategy"] != httpPlacementStrategyExpandCapacityIfNeeded {
		t.Fatalf("placement_strategy: %#v", resp["placement_strategy"])
	}
	if resp["capacity_expanded"] != true {
		t.Fatalf("capacity_expanded: %#v", resp["capacity_expanded"])
	}
	hc, ok := resp["host_capacity_changes"].([]any)
	if !ok || len(hc) < 1 {
		t.Fatalf("expected host_capacity_changes, got %#v", resp["host_capacity_changes"])
	}
	cfg, ok := exec.GetRunConfiguration(rec.Run.Id)
	if !ok || cfg == nil {
		t.Fatal("GetRunConfiguration")
	}
	for _, s := range cfg.Services {
		if s.ServiceId == "svc1" && s.CpuCores != 3.0 {
			t.Fatalf("expected svc1 cpu_cores 3, got %v", s.CpuCores)
		}
	}
}

func TestHTTPServerPatchConfigurationExpandThenAppliesMemoryIncrease(t *testing.T) {
	yaml := `
hosts:
  - id: host-1
    cores: 8
    memory_gb: 1
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
    arrival: {type: poisson, rate_rps: 5}
`
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rec, err := store.Create("run-expand-mem", &simulationv1.RunInput{ScenarioYaml: yaml, DurationMs: 2000})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	recLatest, ok := store.Get(rec.Run.Id)
	if !ok || recLatest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run not RUNNING")
	}
	defer exec.Stop(rec.Run.Id)

	strictBody := map[string]any{
		"services": []map[string]any{{
			"id": "svc1", "replicas": 1, "memory_mb": 2048.0,
		}},
	}
	sb, _ := json.Marshal(strictBody)
	rrStrict := httptest.NewRecorder()
	reqStrict := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-expand-mem/configuration", strings.NewReader(string(sb)))
	reqStrict.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rrStrict, reqStrict)
	if rrStrict.Code != http.StatusInternalServerError {
		t.Fatalf("strict expected 500, got %d: %s", rrStrict.Code, rrStrict.Body.String())
	}

	body := map[string]any{
		"placement_strategy": httpPlacementStrategyExpandCapacityIfNeeded,
		"services": []map[string]any{{
			"id": "svc1", "replicas": 1, "memory_mb": 2048.0,
		}},
	}
	b, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-expand-mem/configuration", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expand expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["capacity_expanded"] != true {
		t.Fatalf("capacity_expanded: %#v", resp["capacity_expanded"])
	}
	cfg, ok := exec.GetRunConfiguration(rec.Run.Id)
	if !ok || cfg == nil {
		t.Fatal("GetRunConfiguration")
	}
	for _, s := range cfg.Services {
		if s.ServiceId == "svc1" && s.MemoryMb != 2048.0 {
			t.Fatalf("expected svc1 memory_mb 2048, got %v", s.MemoryMb)
		}
	}
}

func TestHTTPServerPatchConfigurationExpandDoesNotBypassCPUDecreaseWithActiveWork(t *testing.T) {
	verticalScenarioYAML := strings.Replace(testScenarioYAML, "cores: 2", "cores: 8", 1)
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rec, err := store.Create("run-expand-down", &simulationv1.RunInput{ScenarioYaml: verticalScenarioYAML, DurationMs: 2000})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	recLatest, ok := store.Get(rec.Run.Id)
	if !ok || recLatest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run not RUNNING")
	}
	defer exec.Stop(rec.Run.Id)

	up := map[string]any{"services": []map[string]any{{
		"id": "svc1", "replicas": 1, "cpu_cores": 4.0,
	}}}
	ub, _ := json.Marshal(up)
	rrUp := httptest.NewRecorder()
	reqUp := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-expand-down/configuration", strings.NewReader(string(ub)))
	reqUp.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rrUp, reqUp)
	if rrUp.Code != http.StatusOK {
		t.Fatalf("scale up: %d %s", rrUp.Code, rrUp.Body.String())
	}

	exec.mu.Lock()
	rm := exec.resourceManagers[rec.Run.Id]
	exec.mu.Unlock()
	if rm == nil {
		t.Fatal("nil resource manager")
	}
	insts := rm.GetInstancesForService("svc1")
	if len(insts) != 1 {
		t.Fatalf("instances: %d", len(insts))
	}
	insts[0].AllocateCPU(1, time.Now())

	down := map[string]any{
		"placement_strategy": httpPlacementStrategyExpandCapacityIfNeeded,
		"services": []map[string]any{{
			"id": "svc1", "replicas": 1, "cpu_cores": 1.0,
		}},
	}
	db, _ := json.Marshal(down)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-expand-down/configuration", strings.NewReader(string(db)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on blocked CPU decrease, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "cannot decrease CPU") {
		t.Fatalf("expected decrease guard error, got %s", rr.Body.String())
	}
}

func TestHTTPServerPatchConfigurationInvalidPlacementStrategy400(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rec, err := store.Create("run-bad-ps", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 2000})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	recLatest, ok := store.Get(rec.Run.Id)
	if !ok || recLatest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run not RUNNING")
	}
	defer exec.Stop(rec.Run.Id)

	body := `{"placement_strategy":"relaxed","services":[{"id":"svc1","replicas":1}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-bad-ps/configuration", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "placement_strategy") {
		t.Fatalf("body: %s", rr.Body.String())
	}
}

func TestHTTPServerPatchConfigurationStrictServicesAndWorkloadCompatible(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rec, err := store.Create("run-strict-combo", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 2000})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	recLatest, ok := store.Get(rec.Run.Id)
	if !ok || recLatest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run not RUNNING")
	}
	defer exec.Stop(rec.Run.Id)

	body := map[string]any{
		"placement_strategy": httpPlacementStrategyStrict,
		"services": []map[string]any{{
			"id": "svc1", "replicas": 1,
		}},
		"workload": []map[string]any{{
			"pattern_key": "client:svc1:/test",
			"rate_rps":    7.0,
		}},
	}
	b, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-strict-combo/configuration", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, has := resp["placement_strategy"]; has {
		t.Fatalf("expected default strict response without placement_strategy echo, got %#v", resp)
	}
}

func TestHTTPServerPatchConfigurationExpandWorkloadOnlyReportsNoCapacityChange(t *testing.T) {
	store := NewRunStore()
	exec := NewRunExecutor(store, nil)
	srv := NewHTTPServer(store, exec)

	rec, err := store.Create("run-expand-wl", &simulationv1.RunInput{ScenarioYaml: testScenarioYAML, DurationMs: 2000})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := exec.Start(rec.Run.Id); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	recLatest, ok := store.Get(rec.Run.Id)
	if !ok || recLatest.Run.Status != simulationv1.RunStatus_RUN_STATUS_RUNNING {
		t.Skipf("run not RUNNING")
	}
	defer exec.Stop(rec.Run.Id)

	body := map[string]any{
		"placement_strategy": httpPlacementStrategyExpandCapacityIfNeeded,
		"workload": []map[string]any{{
			"pattern_key": "client:svc1:/test",
			"rate_rps":    12.0,
		}},
	}
	b, _ := json.Marshal(body)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/runs/run-expand-wl/configuration", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["placement_strategy"] != httpPlacementStrategyExpandCapacityIfNeeded {
		t.Fatalf("placement_strategy: %#v", resp["placement_strategy"])
	}
	if resp["capacity_expanded"] != false {
		t.Fatalf("capacity_expanded: %#v", resp["capacity_expanded"])
	}
	if _, has := resp["host_capacity_changes"]; has {
		t.Fatalf("unexpected host_capacity_changes: %#v", resp["host_capacity_changes"])
	}
}
